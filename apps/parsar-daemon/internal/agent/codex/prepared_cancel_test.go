package codex

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func TestPreparedCancelUnusedWaitsForCleanup(t *testing.T) {
	req, cfg, root := preparationFixture(t)
	req.AgentSessionID = "requested-but-unobserved-thread"
	p, err := newPreparation(t.Context(), req, cfg)
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	allowCleanup := sync.OnceFunc(func() { close(release) })
	defer allowCleanup()
	cleanup := p.plan.Cleanup
	var cleanups atomic.Int32
	p.plan.Cleanup = sync.OnceFunc(func() {
		cleanups.Add(1)
		close(entered)
		<-release
		cleanup()
	})
	finished := make(chan error, 4)
	for range 4 {
		go func() { finished <- p.Cancel(context.Background()) }()
	}
	select {
	case <-entered:
	case <-time.After(4 * time.Second):
		t.Fatal("cancellation did not begin cleanup")
	}
	out := make(chan proto.Envelope, 8)
	if s, err := p.Start(t.Context(), "late", "must not execute", out); err == nil || s != nil {
		t.Fatal("cancellation did not fence Start")
	}
	select {
	case <-finished:
		t.Fatal("cancellation returned before unused cleanup")
	default:
	}
	allowCleanup()
	for range 4 {
		select {
		case err := <-finished:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(4 * time.Second):
			t.Fatal("repeated cancellation did not finish")
		}
	}
	if cleanups.Load() != 1 {
		t.Fatal("cleanup ran more than once")
	}
	waitPreparedRelease(t, p, root)
	assertPreparationOnly(t, root)
	assertUnstartedCancellation(t, p)
}

func TestPreparedCancelDuringStartReadiness(t *testing.T) {
	req, cfg, root := preparationFixture(t)
	req.AgentSessionID = "requested-but-unobserved-thread"
	p, err := newPreparation(t.Context(), req, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Cancel(context.Background())
	before := len(preparationFrames(t, root))
	if err := os.WriteFile(filepath.Join(root, "remote-status"), []byte("blocked"), 0o600); err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	out := make(chan proto.Envelope, 8)
	go func() {
		session, err := p.Start(t.Context(), "run", "prompt", out)
		if session != nil {
			t.Error("cancelled readiness returned a Session")
		}
		finished <- err
	}()
	deadline := time.Now().Add(4 * time.Second)
	for len(preparationFrames(t, root)) == before && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	frames := preparationFrames(t, root)
	if len(frames) != before+1 || frames[before].Method != "environment/status" {
		t.Fatal("Start did not enter its readiness recheck")
	}
	if err := p.Cancel(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-finished:
		if err == nil {
			t.Fatal("cancelled Start succeeded")
		}
	case <-time.After(4 * time.Second):
		t.Fatal("cancelled Start remained blocked")
	}
	waitPreparedRelease(t, p, root)
	assertPreparationOnly(t, root)
	assertUnstartedCancellation(t, p)
	if len(out) != 0 {
		t.Fatal("unused resource emitted Run output")
	}
}

func assertUnstartedCancellation(t *testing.T, p *Prepared) {
	t.Helper()
	got := p.CancellationOutcome()
	if got.Content != "" || len(got.Metadata) != 0 || !reflect.DeepEqual(got.Usage, proto.Usage{}) {
		t.Fatalf("unobserved result was invented: %+v", got)
	}
}

func TestPreparedCancelTransferredPreservesObservedOutcome(t *testing.T) {
	req, cfg, root := preparationFixture(t)
	t.Setenv("PARSAR_PREPARATION_OBSERVE", "1")
	p, err := newPreparation(t.Context(), req, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Cancel(context.Background())
	started, err := p.Start(t.Context(), "run", "prompt", make(chan proto.Envelope, 16))
	if err != nil {
		t.Fatal(err)
	}
	s := started.(*Session)
	deadline := time.Now().Add(4 * time.Second)
	for {
		got := p.CancellationOutcome()
		if got.Content == "observed partial answer" && got.Usage.Tokens != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("native output and Usage were not observed")
		}
		time.Sleep(time.Millisecond)
	}
	if err := p.Close(); err != nil || !s.rpc.Alive() {
		t.Fatal("Close cancelled transferred Session", err)
	}
	var calls sync.WaitGroup
	for range 4 {
		calls.Go(func() {
			if err := p.Cancel(context.Background()); err != nil {
				t.Error(err)
			}
		})
	}
	calls.Wait()
	select {
	case <-s.waitDone:
	case <-time.After(4 * time.Second):
		t.Fatal("transferred Session did not finish")
	}
	waitPreparedRelease(t, p, root)
	got := p.CancellationOutcome()
	want := proto.TokenUsage{InputTokens: 30, CachedInputTokens: 4, OutputTokens: 10, ReasoningOutputTokens: 2, TotalTokens: 40}
	if got.Content != "observed partial answer" || got.Metadata[proto.DoneMetaAgentSessionID] != "fixture-native-thread" || got.Usage.Tokens == nil || *got.Usage.Tokens != want {
		t.Fatalf("observed outcome lost: %+v", got)
	}
	if !reflect.DeepEqual(got, s.CancellationOutcome()) {
		t.Fatal("preparation and Session exposed different outcomes")
	}
	interrupts := 0
	for _, frame := range preparationFrames(t, root) {
		if frame.Method == "turn/interrupt" {
			interrupts++
		}
	}
	if interrupts != 1 {
		t.Fatal("native cancellation was missing or repeated", interrupts)
	}
}

func TestPreparedCancelRacingTransfer(t *testing.T) {
	for range 8 {
		req, cfg, root := preparationFixture(t)
		p, err := newPreparation(t.Context(), req, cfg)
		if err != nil {
			t.Fatal(err)
		}
		begin := make(chan struct{})
		var calls sync.WaitGroup
		calls.Go(func() {
			<-begin
			_, _ = p.Start(t.Context(), "run", "prompt", make(chan proto.Envelope, 16))
		})
		calls.Go(func() { <-begin; _ = p.Cancel(context.Background()) })
		calls.Go(func() { <-begin; _ = p.Close() })
		close(begin)
		calls.Wait()
		if err := p.Cancel(context.Background()); err != nil {
			t.Fatal(err)
		}
		if p.started {
			select {
			case <-p.session.waitDone:
			case <-time.After(4 * time.Second):
				t.Fatal("racing Session was retained")
			}
		} else {
			assertPreparationOnly(t, root)
			assertUnstartedCancellation(t, p)
		}
		waitPreparedRelease(t, p, root)
		if s, err := p.Start(t.Context(), "again", "must not execute", make(chan proto.Envelope, 8)); err == nil || s != nil {
			t.Fatal("cancelled preparation started again")
		}
	}
}
