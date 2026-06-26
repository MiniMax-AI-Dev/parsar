package dispatch

// Test-only re-exports so router_test.go (package dispatch_test) can
// peek into internal state without widening the public surface.

// AskIndexLenForTest returns the number of asks still indexed at the
// router level. Used to assert cleanup paths.
func (r *Router) AskIndexLenForTest() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.askIndex)
}

// PendingAsksLenForTest reports how many asks the named run still has
// outstanding. Returns -1 if the run is unknown.
func (r *Router) PendingAsksLenForTest(runID string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.sessions[runID]
	if !ok {
		return -1
	}
	return len(s.pendingAsks)
}
