package daemonize

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestTailReturnsLastNLinesAndExitsWithoutFollow(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tail.log")
	var b strings.Builder
	for i := 1; i <= 10; i++ {
		b.WriteString("line-")
		b.WriteString(itoa(i))
		b.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	var buf bytes.Buffer
	if err := Tail(path, TailOptions{LastLines: 3, Follow: false}, nil, &buf); err != nil {
		t.Fatalf("Tail: %v", err)
	}
	got := buf.String()
	want := "line-8\nline-9\nline-10\n"
	if got != want {
		t.Fatalf("Tail content = %q, want %q", got, want)
	}
}

func TestTailLastLinesZeroPrintsNothingHistorical(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tail.log")
	if err := os.WriteFile(path, []byte("a\nb\nc\n"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	var buf bytes.Buffer
	if err := Tail(path, TailOptions{LastLines: 0, Follow: false}, nil, &buf); err != nil {
		t.Fatalf("Tail: %v", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("Tail wrote %q with LastLines=0, want empty", buf.String())
	}
}

func TestTailLastLinesGreaterThanFilePrintsAll(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tail.log")
	if err := os.WriteFile(path, []byte("a\nb\nc\n"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	var buf bytes.Buffer
	if err := Tail(path, TailOptions{LastLines: 1000, Follow: false}, nil, &buf); err != nil {
		t.Fatalf("Tail: %v", err)
	}
	if buf.String() != "a\nb\nc\n" {
		t.Fatalf("Tail got %q, want full file", buf.String())
	}
}

func TestTailFollowPicksUpAppendedBytes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tail.log")
	if err := os.WriteFile(path, []byte("initial\n"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Thread-safe writer so the appender goroutine's writes and the
	// main goroutine's reads don't race the data race detector.
	w := &safeBuf{}
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = Tail(path, TailOptions{
			LastLines:    100,
			Follow:       true,
			PollInterval: 50 * time.Millisecond,
		}, done, w)
	}()

	// Give Tail time to flush the historical chunk and enter poll loop.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(w.String(), "initial\n") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Append fresh bytes — Tail's next poll tick should pick them up.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("append open: %v", err)
	}
	if _, err := f.WriteString("follow-1\nfollow-2\n"); err != nil {
		t.Fatalf("append write: %v", err)
	}
	_ = f.Close()

	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(w.String(), "follow-2\n") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	close(done)
	wg.Wait()

	out := w.String()
	if !strings.Contains(out, "initial\n") {
		t.Errorf("missing historical chunk; got %q", out)
	}
	if !strings.Contains(out, "follow-1\n") || !strings.Contains(out, "follow-2\n") {
		t.Errorf("missing appended bytes; got %q", out)
	}
}

func TestTailErrorsOnMissingFile(t *testing.T) {
	var buf bytes.Buffer
	err := Tail(filepath.Join(t.TempDir(), "nope.log"), TailOptions{LastLines: 1}, nil, &buf)
	if err == nil {
		t.Fatalf("Tail on missing file succeeded")
	}
}

func TestEnsureLogFileCreatesEmpty0600(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fresh.log")
	if err := EnsureLogFile(path); err != nil {
		t.Fatalf("EnsureLogFile: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Size() != 0 {
		t.Errorf("size = %d, want 0", info.Size())
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %o, want 0600", info.Mode().Perm())
	}
}

func TestEnsureLogFileIdempotentOnExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "exists.log")
	if err := os.WriteFile(path, []byte("preexisting\n"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := EnsureLogFile(path); err != nil {
		t.Fatalf("EnsureLogFile: %v", err)
	}
	body, _ := os.ReadFile(path)
	if string(body) != "preexisting\n" {
		t.Errorf("body = %q, want preexisting preserved", string(body))
	}
}

func TestEnsureLogFileCreatesMissingParentDir(t *testing.T) {
	// Regression: first-ever `parsar-daemon connect -b` on a host without
	// ~/.parsar/parsar-daemon/<profile>/ used to fail with ENOENT —
	// O_CREATE only creates the file leaf.
	dir := t.TempDir()
	path := filepath.Join(dir, "missing", "deeper", "fresh.log")
	if err := EnsureLogFile(path); err != nil {
		t.Fatalf("EnsureLogFile on missing parent: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("stat after EnsureLogFile: %v", err)
	}
}

func TestMustWriteLineAppendsTrailingNewline(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ml.log")
	if err := MustWriteLine(path, "no-newline"); err != nil {
		t.Fatalf("MustWriteLine: %v", err)
	}
	if err := MustWriteLine(path, "has-newline\n"); err != nil {
		t.Fatalf("MustWriteLine: %v", err)
	}
	body, _ := os.ReadFile(path)
	if string(body) != "no-newline\nhas-newline\n" {
		t.Errorf("body = %q", string(body))
	}
}

// safeBuf wraps bytes.Buffer with a mutex so concurrent reads/writes
// during Tail's poll loop don't race.
type safeBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *safeBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *safeBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// itoa avoids pulling strconv into this package's test deps.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
