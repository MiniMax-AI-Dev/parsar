//go:build unix

package dev

import (
	"context"
	"syscall"
	"time"
)

type defaultSkillPreviewRunner struct{}

func (defaultSkillPreviewRunner) Run(ctx context.Context, dir string, name string, args ...string) ([]byte, error) {
	cmd, err := newSkillInstallCommand(ctx, dir, name, args...)
	if err != nil {
		return nil, err
	}
	// The CLI clones via os.tmpdir(), independently of its working directory.
	cmd.Env = append(cmd.Env, "TMPDIR="+dir, "TMP="+dir, "TEMP="+dir)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		// npx launches Node and git; killing only npx leaves their output pipes
		// open and lets downloads outlive the request directory.
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = time.Second
	return cmd.CombinedOutput()
}
