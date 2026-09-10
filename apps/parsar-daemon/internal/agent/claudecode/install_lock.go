package claudecode

import (
	"context"
	"path/filepath"
	"sync"
)

type installRootLock struct {
	token chan struct{}
	users int
}

var installRoots = struct {
	sync.Mutex
	entries map[string]*installRootLock
}{entries: make(map[string]*installRootLock)}

func lockInstallRoot(ctx context.Context, root string) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	key, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	installRoots.Lock()
	entry := installRoots.entries[key]
	if entry == nil {
		entry = &installRootLock{token: make(chan struct{}, 1)}
		entry.token <- struct{}{}
		installRoots.entries[key] = entry
	}
	entry.users++
	installRoots.Unlock()

	release := func() {
		installRoots.Lock()
		entry.users--
		if entry.users == 0 {
			delete(installRoots.entries, key)
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
