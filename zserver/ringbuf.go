package zserver

import "sync"

const ringBufSize = 64 * 1024 // 64 KB replay buffer

type ringBuf struct {
	mu   sync.Mutex
	data []byte
}

func newRingBuf() *ringBuf { return &ringBuf{} }

func (r *ringBuf) write(p []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.data = append(r.data, p...)
	if len(r.data) > ringBufSize {
		// Copy into a fresh slice so the old backing array can be GC'd after bursts.
		retained := make([]byte, ringBufSize)
		copy(retained, r.data[len(r.data)-ringBufSize:])
		r.data = retained
	}
}

func (r *ringBuf) snapshot() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]byte, len(r.data))
	copy(out, r.data)
	return out
}
