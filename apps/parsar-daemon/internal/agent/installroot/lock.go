// Package installroot coordinates adapter installations within one daemon process.
package installroot

import (
	"context"
	"os"
	"sync"
)

type installRootLock struct {
	info  os.FileInfo
	token chan struct{}
	users int
}

var installRoots = struct {
	sync.Mutex
	entries map[*installRootLock]struct{}
}{entries: make(map[*installRootLock]struct{})}

// Lock creates the install root if needed and holds its filesystem identity
// until the returned function is called once.
// Waiting callers may cancel without interrupting the current installation.
func Lock(ctx context.Context, root string) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	installRoots.Lock()
	var entry *installRootLock
	for candidate := range installRoots.entries {
		if os.SameFile(candidate.info, info) {
			entry = candidate
			break
		}
	}
	if entry == nil {
		entry = &installRootLock{info: info, token: make(chan struct{}, 1)}
		entry.token <- struct{}{}
		installRoots.entries[entry] = struct{}{}
	}
	entry.users++
	installRoots.Unlock()

	release := func() {
		installRoots.Lock()
		entry.users--
		if entry.users == 0 {
			delete(installRoots.entries, entry)
		}
		installRoots.Unlock()
	}
	select {
	case <-ctx.Done():
		release()
		return nil, ctx.Err()
	case <-entry.token:
		return func() {
			entry.token <- struct{}{}
			release()
		}, nil
	}
}
