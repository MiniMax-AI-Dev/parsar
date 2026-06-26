package daemonize

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// TailOptions configures Tail.
type TailOptions struct {
	// LastLines is the number of trailing lines to print before any
	// follow logic kicks in. 0 → print nothing historical, jump
	// straight to follow.
	LastLines int

	// Follow keeps the tail open after printing LastLines, polling
	// for new bytes appended by a still-running daemon. false →
	// return immediately once the historical chunk is flushed.
	Follow bool

	// PollInterval is the cadence at which Tail re-stats the file
	// looking for growth. Zero → 500ms.
	PollInterval time.Duration
}

// Tail prints the last opts.LastLines lines of path to w, optionally
// following the file (poll-on-stat) until ctxDone fires. Designed for
// human eyes — not a high-throughput log aggregator.
//
// Pass a nil ctxDone when Follow is false.
func Tail(path string, opts TailOptions, ctxDone <-chan struct{}, w io.Writer) error {
	if path == "" {
		return errors.New("daemonize.Tail: empty path")
	}
	if opts.PollInterval <= 0 {
		opts.PollInterval = 500 * time.Millisecond
	}

	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("daemonize.Tail: open %s: %w", path, err)
	}
	defer f.Close()

	startOffset, err := lastLinesOffset(f, opts.LastLines)
	if err != nil {
		return fmt.Errorf("daemonize.Tail: seek tail: %w", err)
	}
	if _, err := f.Seek(startOffset, io.SeekStart); err != nil {
		return fmt.Errorf("daemonize.Tail: seek: %w", err)
	}

	// Stream the historical chunk through bufio for nice line-buffered
	// behaviour rather than byte-by-byte writes.
	if _, err := io.Copy(w, f); err != nil {
		return fmt.Errorf("daemonize.Tail: read historical: %w", err)
	}

	if !opts.Follow {
		return nil
	}

	pollTicker := time.NewTicker(opts.PollInterval)
	defer pollTicker.Stop()

	for {
		select {
		case <-ctxDone:
			return nil
		case <-pollTicker.C:
			if _, err := io.Copy(w, f); err != nil {
				return fmt.Errorf("daemonize.Tail: follow read: %w", err)
			}
		}
	}
}

// lastLinesOffset returns the byte offset in f from which reading to
// EOF yields the trailing n newline-terminated lines. Returns 0 when
// n <= 0, file has fewer than n lines, or seeking back isn't usable.
func lastLinesOffset(f *os.File, n int) (int64, error) {
	if n <= 0 {
		// Tail from end so follow-mode only shows fresh bytes.
		end, err := f.Seek(0, io.SeekEnd)
		if err != nil {
			return 0, err
		}
		return end, nil
	}

	stat, err := f.Stat()
	if err != nil {
		return 0, err
	}
	size := stat.Size()
	if size == 0 {
		return 0, nil
	}

	// Read backwards in 8 KiB chunks counting newlines. Cap at 1 MiB
	// so a pathological 1 GiB logfile doesn't lock up the process.
	const chunk = 8 * 1024
	const maxScan = 1 * 1024 * 1024
	pos := size
	newlines := 0
	buf := make([]byte, chunk)
	scanned := int64(0)
	for pos > 0 && scanned < maxScan {
		readSize := min(int64(chunk), pos)
		pos -= readSize
		scanned += readSize
		if _, err := f.ReadAt(buf[:readSize], pos); err != nil && !errors.Is(err, io.EOF) {
			return 0, err
		}
		// Walk back-to-front; we want the offset just AFTER the
		// (n+1)th newline so the historical print starts on a line
		// boundary.
		for i := readSize - 1; i >= 0; i-- {
			if buf[i] != '\n' {
				continue
			}
			newlines++
			if newlines > n {
				return pos + i + 1, nil
			}
		}
	}
	return 0, nil
}

// EnsureLogFile creates the log file (0o600) if missing so a
// `parsar-daemon logs` before the first `connect -b` gets "0 bytes"
// instead of "no such file".
func EnsureLogFile(path string) error {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("daemonize.EnsureLogFile: %w", err)
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("daemonize.EnsureLogFile: %w", err)
	}
	return f.Close()
}

// MustWriteLine appends one line to path with 0o600 mode, adding a
// trailing newline if missing. Returns errors despite the name —
// kept short because it's used in startup hot paths.
func MustWriteLine(path string, line string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	bw := bufio.NewWriter(f)
	if _, err := bw.WriteString(line); err != nil {
		return err
	}
	if len(line) == 0 || line[len(line)-1] != '\n' {
		if _, err := bw.WriteString("\n"); err != nil {
			return err
		}
	}
	return bw.Flush()
}
