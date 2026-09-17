package claudesdk

import "bufio"

type bridgeOutput struct {
	frames  chan []byte
	current []byte
	err     error
}

func (s *session) bridgeOutput() *bridgeOutput {
	output := &bridgeOutput{frames: make(chan []byte, 1)}
	go func() {
		defer close(output.frames)
		scanner := bufio.NewScanner(s.process.Stdout)
		scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
		for scanner.Scan() {
			raw := append([]byte(nil), scanner.Bytes()...)
			if !s.receiveWorkspaceRead(raw) {
				output.frames <- raw
			}
		}
		output.err = scanner.Err()
		if output.err != nil {
			s.process.Cancel()
		}
	}()
	return output
}
func (o *bridgeOutput) Scan() bool    { var ok bool; o.current, ok = <-o.frames; return ok }
func (o *bridgeOutput) Bytes() []byte { return o.current }
func (o *bridgeOutput) Err() error    { return o.err }
