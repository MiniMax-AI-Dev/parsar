package dispatch

func (r *Router) closePreparationResource(p *preparationState) error {
	// Only the operation that owns busy calls this; other paths cancel its owner.
	var err error
	if p.prepared != nil {
		err = p.prepared.Close()
	}
	r.mu.Lock()
	p.busy, p.closeErr = false, err
	if err == nil {
		p.prepared, p.owns = nil, false
	}
	r.mu.Unlock()
	if err != nil {
		r.log.Warn("preparation cleanup incomplete", "handle", p.status.Handle)
	}
	return err
}

func (r *Router) closePendingPreparationsLocked() []*preparationState {
	var closeNow []*preparationState
	for _, p := range r.preparations {
		p.timer.Stop()
		if !p.owns {
			continue
		}
		p.cancel()
		switch p.status.State {
		case "preparing", "ready", "starting":
			p.status.State, p.status.ErrorCode, p.status.Revision = "failed", "connection_closed", p.status.Revision+1
		}
		if !p.busy {
			p.busy = true
			r.shutdownWG.Add(1)
			closeNow = append(closeNow, p)
		}
	}
	return closeNow
}
