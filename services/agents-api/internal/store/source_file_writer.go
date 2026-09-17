package store

import (
	"crypto/sha256"
	"hash"
	"io"
)

const sourceFileChunkBytes = 256 << 10

type sourceFileWriter struct {
	body io.Writer
	hash hash.Hash
	size int64
	err  error
}

func newSourceFileWriter(body io.Writer) *sourceFileWriter {
	return &sourceFileWriter{body: body, hash: sha256.New()}
}

func (w *sourceFileWriter) Write(p []byte) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	if int64(len(p)) > MaxSourceFileBytes-w.size {
		w.err = ErrSourceFileTooLarge
		return 0, w.err
	}
	written := 0
	for len(p) > 0 {
		chunk := p[:min(len(p), sourceFileChunkBytes)]
		n, err := w.body.Write(chunk)
		w.hash.Write(chunk[:n])
		w.size += int64(n)
		written += n
		if err == nil && n != len(chunk) {
			err = io.ErrShortWrite
		}
		if err != nil {
			w.err = err
			return written, err
		}
		p = p[n:]
	}
	return written, nil
}
