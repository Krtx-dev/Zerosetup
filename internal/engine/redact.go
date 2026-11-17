package engine

import (
	"bytes"
	"io"
	"sync"
)

const redacted = "[REDACTED]"

// Redactor replaces known secret values in a byte stream. It buffers
// partial lines so a secret split across writes is still caught.
type Redactor struct {
	mu      sync.Mutex
	w       io.Writer
	secrets [][]byte
	buf     []byte
}

// NewRedactor wraps w. Values shorter than 4 bytes are ignored to avoid
// shredding ordinary output.
func NewRedactor(w io.Writer, secrets []string) *Redactor {
	r := &Redactor{w: w}
	for _, s := range secrets {
		if len(s) >= 4 {
			r.secrets = append(r.secrets, []byte(s))
		}
	}
	return r
}

// RedactString redacts a complete string.
func RedactString(s string, secrets []string) string {
	b := []byte(s)
	for _, x := range secrets {
		if len(x) >= 4 {
			b = bytes.ReplaceAll(b, []byte(x), []byte(redacted))
		}
	}
	return string(b)
}

func (r *Redactor) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf = append(r.buf, p...)
	i := bytes.LastIndexByte(r.buf, '\n')
	if i < 0 {
		if len(r.buf) < 64<<10 {
			return len(p), nil
		}
		i = len(r.buf) - 1
	}
	out := r.scrub(r.buf[:i+1])
	r.buf = append(r.buf[:0], r.buf[i+1:]...)
	_, err := r.w.Write(out)
	return len(p), err
}

// Flush writes any buffered partial line.
func (r *Redactor) Flush() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.buf) == 0 {
		return nil
	}
	_, err := r.w.Write(r.scrub(r.buf))
	r.buf = r.buf[:0]
	return err
}

func (r *Redactor) scrub(b []byte) []byte {
	out := append([]byte(nil), b...)
	for _, s := range r.secrets {
		out = bytes.ReplaceAll(out, s, []byte(redacted))
	}
	return out
}
