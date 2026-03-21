package zserver

import "sync"

const ringBufSize = 64 * 1024 // 64 KB replay buffer

// ringBuf is a fixed-size circular byte buffer used for PTY replay.
// Writes never allocate; snapshot copies the live window.
type ringBuf struct {
	mu    sync.Mutex
	buf   [ringBufSize]byte
	pos   int // next write position (modulo ringBufSize)
	total int // total bytes written (used to detect wrap)
}

func newRingBuf() *ringBuf { return &ringBuf{} }

func (r *ringBuf) write(p []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for len(p) > 0 {
		n := copy(r.buf[r.pos:], p)
		r.pos = (r.pos + n) % ringBufSize
		r.total += n
		p = p[n:]
	}
}

func (r *ringBuf) snapshot() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.total == 0 {
		return nil
	}
	if r.total <= ringBufSize {
		// Buffer hasn't wrapped — return [0, pos).
		out := make([]byte, r.pos)
		copy(out, r.buf[:r.pos])
		return out
	}
	// Buffer has wrapped — oldest data starts at pos.
	out := make([]byte, ringBufSize)
	n := copy(out, r.buf[r.pos:])
	copy(out[n:], r.buf[:r.pos])
	return out
}
