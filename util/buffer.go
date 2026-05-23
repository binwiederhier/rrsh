package util

import "bytes"

// CappedBuffer is an io.Writer that silently drops bytes past a fixed
// limit. Write always reports full consumption (so exec.Cmd's
// short-write detection stays quiet) and sets Truncated. Intended for
// bounding untrusted subprocess output where DoS protection beats
// either blocking or erroring.
type CappedBuffer struct {
	buf       bytes.Buffer
	limit     int
	truncated bool
}

// NewCappedBuffer returns a CappedBuffer keeping at most limit bytes.
// A non-positive limit drops every write.
func NewCappedBuffer(limit int) *CappedBuffer {
	return &CappedBuffer{limit: limit}
}

// Write always returns len(p), nil; bytes past the cap are dropped.
func (c *CappedBuffer) Write(p []byte) (int, error) {
	if c.truncated {
		return len(p), nil
	}
	remaining := c.limit - c.buf.Len()
	if remaining <= 0 {
		c.truncated = true
		return len(p), nil
	}
	if len(p) > remaining {
		c.buf.Write(p[:remaining])
		c.truncated = true
		return len(p), nil
	}
	c.buf.Write(p)
	return len(p), nil
}

// Bytes returns the captured bytes (valid until the next Write).
func (c *CappedBuffer) Bytes() []byte { return c.buf.Bytes() }

// Truncated reports whether any byte was dropped.
func (c *CappedBuffer) Truncated() bool { return c.truncated }
