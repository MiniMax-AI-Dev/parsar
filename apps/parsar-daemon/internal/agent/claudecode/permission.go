package claudecode

import "sync"

// pendingTable maps daemon-minted perm_<8hex> ids to the originating
// Claude Code control_request.request_id. Both directions are needed:
//   - SubmitPermission from the gateway looks up cc_request_id (and
//     original input) so we can write a valid control_response.
//   - A control_cancel_request from claude stdout needs the reverse
//     translation so the gateway sees a matching permission_cancel.
type pendingTable struct {
	mu      sync.Mutex
	byPerm  map[string]pendingEntry
	byCCReq map[string]string
}

type pendingEntry struct {
	CCRequestID string
	Input       map[string]any
}

func newPendingTable() *pendingTable {
	return &pendingTable{
		byPerm:  make(map[string]pendingEntry),
		byCCReq: make(map[string]string),
	}
}

// Record links a freshly minted perm_id to the originating
// cc_request_id and the tool-call input the human is being asked to
// approve.
func (p *pendingTable) Record(permID, ccRequestID string, input map[string]any) {
	if permID == "" || ccRequestID == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.byPerm[permID] = pendingEntry{CCRequestID: ccRequestID, Input: input}
	p.byCCReq[ccRequestID] = permID
}

// Resolve returns the entry recorded for permID. ok is false when
// permID is unknown or already Delete-d.
func (p *pendingTable) Resolve(permID string) (pendingEntry, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	e, ok := p.byPerm[permID]
	return e, ok
}

// LookupByCC reverses the mapping for control_cancel_request handling.
func (p *pendingTable) LookupByCC(ccRequestID string) (string, bool) {
	if ccRequestID == "" {
		return "", false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	permID, ok := p.byCCReq[ccRequestID]
	return permID, ok
}

// Delete removes both directions for permID.
func (p *pendingTable) Delete(permID string) {
	if permID == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	e, ok := p.byPerm[permID]
	if !ok {
		return
	}
	delete(p.byPerm, permID)
	delete(p.byCCReq, e.CCRequestID)
}

// Len reports the number of outstanding permissions.
func (p *pendingTable) Len() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.byPerm)
}
