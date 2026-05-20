package util

import "testing"

func TestCappedBuffer_BelowLimit(t *testing.T) {
	t.Parallel()
	cb := NewCappedBuffer(16)
	n, err := cb.Write([]byte("hello"))
	if err != nil {
		t.Fatalf("Write returned error: %v", err)
	}
	if n != 5 {
		t.Errorf("Write returned %d, want 5", n)
	}
	if cb.Truncated() {
		t.Error("Truncated() should be false when below limit")
	}
	if string(cb.Bytes()) != "hello" {
		t.Errorf("Bytes() = %q, want %q", cb.Bytes(), "hello")
	}
}

func TestCappedBuffer_ExactLimit(t *testing.T) {
	t.Parallel()
	cb := NewCappedBuffer(5)
	cb.Write([]byte("hello"))
	if cb.Truncated() {
		t.Error("Truncated() should be false at exactly the limit")
	}
	if string(cb.Bytes()) != "hello" {
		t.Errorf("Bytes() = %q, want %q", cb.Bytes(), "hello")
	}
}

func TestCappedBuffer_SingleWriteOverflow(t *testing.T) {
	t.Parallel()
	cb := NewCappedBuffer(4)
	n, _ := cb.Write([]byte("12345678"))
	if n != 8 {
		t.Errorf("Write returned %d, want 8 (full input length even when truncated)", n)
	}
	if !cb.Truncated() {
		t.Error("Truncated() should be true after overflow")
	}
	if string(cb.Bytes()) != "1234" {
		t.Errorf("Bytes() = %q, want %q", cb.Bytes(), "1234")
	}
}

func TestCappedBuffer_OverflowAcrossMultipleWrites(t *testing.T) {
	t.Parallel()
	cb := NewCappedBuffer(6)
	cb.Write([]byte("hell"))
	cb.Write([]byte("oworld"))
	if !cb.Truncated() {
		t.Error("Truncated() should be true after second write spills past cap")
	}
	if string(cb.Bytes()) != "hellow" {
		t.Errorf("Bytes() = %q, want %q", cb.Bytes(), "hellow")
	}
}

func TestCappedBuffer_PostTruncateWritesDropped(t *testing.T) {
	t.Parallel()
	cb := NewCappedBuffer(2)
	cb.Write([]byte("abc"))
	if !cb.Truncated() {
		t.Fatal("expected truncated after first overflow")
	}
	n, _ := cb.Write([]byte("more"))
	if n != 4 {
		t.Errorf("post-truncate Write returned %d, want 4", n)
	}
	if string(cb.Bytes()) != "ab" {
		t.Errorf("Bytes() = %q, want %q", cb.Bytes(), "ab")
	}
}

func TestCappedBuffer_ZeroLimitDropsAll(t *testing.T) {
	t.Parallel()
	cb := NewCappedBuffer(0)
	n, _ := cb.Write([]byte("anything"))
	if n != 8 {
		t.Errorf("Write returned %d, want 8", n)
	}
	if !cb.Truncated() {
		t.Error("Truncated() should be true with zero-cap buffer")
	}
	if len(cb.Bytes()) != 0 {
		t.Errorf("Bytes() length = %d, want 0", len(cb.Bytes()))
	}
}
