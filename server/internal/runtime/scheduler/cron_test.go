package scheduler

import (
	"testing"
	"time"
)

func TestNextRunRespectsTimezone(t *testing.T) {
	// 2026-06-27 00:00 UTC = 2026-06-27 08:00 Asia/Shanghai (UTC+8).
	// cron "0 9 * * *" in Shanghai → next is 09:00 CST = 01:00 UTC same day.
	after := time.Date(2026, 6, 27, 0, 0, 0, 0, time.UTC)
	got, err := NextRun("0 9 * * *", "Asia/Shanghai", after)
	if err != nil {
		t.Fatalf("NextRun: %v", err)
	}
	want := time.Date(2026, 6, 27, 1, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("got %s want %s", got, want)
	}
	if got.Location() != time.UTC {
		t.Fatalf("expected UTC result, got %s", got.Location())
	}
}

func TestNextRunRejectsBadInput(t *testing.T) {
	if _, err := NextRun("not a cron", "UTC", time.Now()); err == nil {
		t.Fatal("expected error for bad cron")
	}
	if _, err := NextRun("0 9 * * *", "Mars/Phobos", time.Now()); err == nil {
		t.Fatal("expected error for bad timezone")
	}
}
