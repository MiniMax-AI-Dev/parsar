package codex

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Close initiates shutdown once and waits for the owned child to be reaped.
func (c *JSONRPCClient) Close() error {
	defer func() { _ = c.drainPending(errors.New("codex rpc: client closed")) }()
	c.closeOnce.Do(func() {
		c.mu.Lock()
		cmd := c.cmd
		stdin := c.stdin
		c.alive = false
		c.mu.Unlock()
		if stdin != nil {
			_ = stdin.Close()
		}
		if cmd != nil && cmd.Process != nil {
			grace := time.NewTimer(250 * time.Millisecond)
			defer grace.Stop()
			select {
			case <-c.doneCh:
			case <-grace.C:
				_ = cmd.Process.Kill()
			}
		}
	})
	c.mu.Lock()
	cmd := c.cmd
	c.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	wait := time.NewTimer(rpcKillTimeout)
	defer wait.Stop()
	select {
	case <-c.doneCh:
		return nil
	case <-wait.C:
		select {
		case <-c.doneCh:
			return nil
		default:
			c.cfg.Logger.Warn("codex rpc child did not exit after kill", "tag", c.cfg.LogTag)
			return fmt.Errorf("codex rpc: waiting for child exit: %w", context.DeadlineExceeded)
		}
	}
}
