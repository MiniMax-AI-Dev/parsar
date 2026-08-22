package enginehost

import (
	"strings"
	"sync"
)

// lineTail is a fixed-size ring of the most recent output lines. Engine
// boot failures are explained by the last few lines, so the tail is
// bounded rather than accumulating a whole session's chatter in memory.
type lineTail struct {
	mu    sync.Mutex
	limit int
	lines []string
}

func newLineTail(limit int) *lineTail {
	if limit <= 0 {
		limit = DefaultStderrLines
	}
	return &lineTail{limit: limit, lines: make([]string, 0, limit)}
}

func (t *lineTail) push(line string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.lines) == t.limit {
		copy(t.lines, t.lines[1:])
		t.lines = t.lines[:t.limit-1]
	}
	t.lines = append(t.lines, line)
}

func (t *lineTail) String() string {
	if t == nil {
		return ""
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.Join(t.lines, " | ")
}
