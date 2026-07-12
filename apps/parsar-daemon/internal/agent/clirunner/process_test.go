package clirunner

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestStartWaitClosesDone(t *testing.T) {
	proc, err := Start(StartOptions{
		Parent: context.Background(),
		Binary: helperBinary(),
		Args:   []string{"-test.run=TestHelperProcess", "--", "exit"},
		Env:    helperEnv(),
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := proc.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	select {
	case <-proc.Done():
	case <-time.After(time.Second):
		t.Fatal("Done did not close")
	}
}

func TestCancelCancelsContext(t *testing.T) {
	proc, err := Start(StartOptions{
		Parent:      context.Background(),
		Binary:      helperBinary(),
		Args:        []string{"-test.run=TestHelperProcess", "--", "sleep"},
		Env:         helperEnv(),
		KillTimeout: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	proc.Cancel()
	select {
	case <-proc.Context().Done():
	case <-time.After(time.Second):
		t.Fatal("context was not cancelled")
	}
	_ = proc.Wait()
}

func helperBinary() string {
	return os.Args[0]
}

func helperEnv() []string {
	return append(os.Environ(), "GO_WANT_HELPER_PROCESS=1")
}

func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	args := os.Args
	for len(args) > 0 && args[0] != "--" {
		args = args[1:]
	}
	if len(args) < 2 {
		os.Exit(2)
	}
	switch args[1] {
	case "exit":
		os.Exit(0)
	case "sleep":
		time.Sleep(10 * time.Second)
		os.Exit(0)
	default:
		os.Exit(2)
	}
}
