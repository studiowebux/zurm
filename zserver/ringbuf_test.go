package zserver

import (
	"bytes"
	"testing"
)

func TestRingBuf_Empty(t *testing.T) {
	rb := newRingBuf()
	if got := rb.snapshot(); got != nil {
		t.Errorf("empty snapshot = %v, want nil", got)
	}
}

func TestRingBuf_SmallWrite(t *testing.T) {
	rb := newRingBuf()
	rb.write([]byte("hello"))
	got := rb.snapshot()
	if !bytes.Equal(got, []byte("hello")) {
		t.Errorf("snapshot = %q, want %q", got, "hello")
	}
}

func TestRingBuf_MultipleWrites(t *testing.T) {
	rb := newRingBuf()
	rb.write([]byte("hello"))
	rb.write([]byte(" world"))
	got := rb.snapshot()
	if !bytes.Equal(got, []byte("hello world")) {
		t.Errorf("snapshot = %q, want %q", got, "hello world")
	}
}

func TestRingBuf_ExactCapacity(t *testing.T) {
	rb := newRingBuf()
	data := make([]byte, ringBufSize)
	for i := range data {
		data[i] = byte(i % 256)
	}
	rb.write(data)
	got := rb.snapshot()
	if !bytes.Equal(got, data) {
		t.Errorf("exact capacity: snapshot length = %d, want %d", len(got), ringBufSize)
	}
}

func TestRingBuf_WrapAround(t *testing.T) {
	rb := newRingBuf()
	// Fill the buffer completely.
	filler := make([]byte, ringBufSize)
	for i := range filler {
		filler[i] = 'A'
	}
	rb.write(filler)
	// Write 10 more bytes — these overwrite the oldest.
	extra := []byte("0123456789")
	rb.write(extra)

	got := rb.snapshot()
	if len(got) != ringBufSize {
		t.Fatalf("snapshot length = %d, want %d", len(got), ringBufSize)
	}
	// Last 10 bytes should be the extra data.
	tail := got[ringBufSize-10:]
	if !bytes.Equal(tail, extra) {
		t.Errorf("tail = %q, want %q", tail, extra)
	}
	// First bytes should be 'A' (the non-overwritten portion).
	if got[0] != 'A' {
		t.Errorf("first byte = %c, want A", got[0])
	}
}

func TestRingBuf_MultipleWraps(t *testing.T) {
	rb := newRingBuf()
	// Write 3x the buffer size.
	chunk := make([]byte, ringBufSize)
	for i := range chunk {
		chunk[i] = 'X'
	}
	rb.write(chunk)
	rb.write(chunk)

	// Third write with distinct data.
	final := make([]byte, ringBufSize)
	for i := range final {
		final[i] = byte('a' + i%26)
	}
	rb.write(final)

	got := rb.snapshot()
	if !bytes.Equal(got, final) {
		t.Error("after 3x wrap, snapshot should contain only the last write")
	}
}

func TestRingBuf_PartialOverwrite(t *testing.T) {
	rb := newRingBuf()
	// Write exactly half the buffer.
	half := make([]byte, ringBufSize/2)
	for i := range half {
		half[i] = 'A'
	}
	rb.write(half)

	// Write another full buffer — wraps past the first half.
	full := make([]byte, ringBufSize)
	for i := range full {
		full[i] = 'B'
	}
	rb.write(full)

	got := rb.snapshot()
	if len(got) != ringBufSize {
		t.Fatalf("len = %d, want %d", len(got), ringBufSize)
	}
	// Should be all B's (full buffer of B's is the latest data).
	for i, b := range got {
		if b != 'B' {
			t.Errorf("byte[%d] = %c, want B", i, b)
			break
		}
	}
}

func TestRingBuf_SnapshotIsACopy(t *testing.T) {
	rb := newRingBuf()
	rb.write([]byte("original"))
	snap := rb.snapshot()
	// Mutate the snapshot.
	snap[0] = 'X'
	// Re-snapshot should still show original.
	got := rb.snapshot()
	if got[0] != 'o' {
		t.Error("snapshot mutation affected ring buffer")
	}
}

func TestRingBuf_SingleByte(t *testing.T) {
	rb := newRingBuf()
	rb.write([]byte{42})
	got := rb.snapshot()
	if len(got) != 1 || got[0] != 42 {
		t.Errorf("got %v, want [42]", got)
	}
}

func TestRingBuf_TotalTracking(t *testing.T) {
	rb := newRingBuf()
	rb.write([]byte("abc"))
	if rb.total != 3 {
		t.Errorf("total = %d, want 3", rb.total)
	}
	rb.write([]byte("de"))
	if rb.total != 5 {
		t.Errorf("total = %d, want 5", rb.total)
	}
}
