package codex

import "context"

func (c *JSONRPCClient) writeFrameContext(ctx context.Context, frame any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- c.writeFrame(frame) }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		select {
		case err := <-done:
			return err
		default:
		}
		// A partial frame cannot be retracted. Close only a blocked write;
		// a response timeout after a complete write leaves the process alive.
		_ = c.Close()
		<-done
		return ctx.Err()
	}
}
