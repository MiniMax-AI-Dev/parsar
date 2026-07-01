package scheduler

import (
	"fmt"
	"time"

	"github.com/robfig/cron/v3"
)

// NextRun returns the next fire time strictly after `after`, in UTC,
// interpreting cronExpr (standard 5-field) in the given IANA timezone.
func NextRun(cronExpr, timezone string, after time.Time) (time.Time, error) {
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return time.Time{}, fmt.Errorf("scheduler: bad timezone %q: %w", timezone, err)
	}
	sched, err := cron.ParseStandard(cronExpr)
	if err != nil {
		return time.Time{}, fmt.Errorf("scheduler: bad cron %q: %w", cronExpr, err)
	}
	next := sched.Next(after.In(loc))
	if next.IsZero() {
		return time.Time{}, fmt.Errorf("scheduler: cron %q has no next occurrence", cronExpr)
	}
	return next.UTC(), nil
}
