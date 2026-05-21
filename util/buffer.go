package util

import "bytes"

// CappedBuffer is an io.Writer backed by a bytes.Buffer that silently
// drops bytes once a fixed limit is reached. Writes always claim to have
// consumed the entire input (so they don't trip exec.Cmd's short-write
// detection) but bytes past the limit are not retained. The Truncated
// method returns true if any write was dropped.
//
// Intended for capturing untrusted subprocess output where unbounded
// growth is a DoS risk and partial capture is preferable to either
// blocking or returning an error.
type CappedBuffer struct {
	buf       bytes.Buffer
	limit     int
	truncated bool
}

// NewCappedBuffer returns a CappedBuffer that retains at most limit
// bytes. A limit of zero or negative effectively makes every write
// dropped while still reporting full consumption.
func NewCappedBuffer(limit int) *CappedBuffer {
	return &CappedBuffer{limit: limit}
}

// Write implements io.Writer. It always returns len(p), nil - bytes past
// the cap are dropped and the truncated flag is set.
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

// Bytes returns the captured bytes. The slice is valid until the next
// Write, matching bytes.Buffer semantics.
func (c *CappedBuffer) Bytes() []byte { return c.buf.Bytes() }

// Truncated reports whether any byte was dropped because the cap was hit.
func (c *CappedBuffer) Truncated() bool { return c.truncated }
