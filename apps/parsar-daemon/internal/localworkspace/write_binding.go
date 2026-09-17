package localworkspace

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type fileWriter struct {
	helper    string
	staging   string
	mu        sync.Mutex
	uncertain bool
}

// bindWriter runs only during startup. Path checks do not prove native isolation;
// deployment qualification must deny tools access to staging and its ancestors.
func (b *Binding) bindWriter(helper, staging string) error {
	invalid := errors.New("local file writer requires a protected sibling staging directory and external executable")
	parent := filepath.Dir(b.workspace)
	if parent == "/" || filepath.Dir(staging) != parent || staging == b.workspace {
		return invalid
	}
	for _, name := range []string{b.workspace, staging, helper} {
		if !filepath.IsAbs(name) || filepath.Clean(name) != name || strings.ContainsAny(name, "\x00\r\n\\") {
			return invalid
		}
		resolved, err := filepath.EvalSymlinks(name)
		if err != nil || resolved != name {
			return invalid
		}
	}
	dir, err := os.Stat(staging)
	if err != nil || !dir.IsDir() {
		return invalid
	}
	program, err := os.Stat(helper)
	if err != nil || !program.Mode().IsRegular() || program.Mode().Perm()&0111 == 0 || helper == parent || strings.HasPrefix(helper, parent+string(filepath.Separator)) {
		return invalid
	}
	b.writer = &fileWriter{helper: helper, staging: staging}
	return nil
}
