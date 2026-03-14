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
		r.data = r.data[len(r.data)-ringBufSize:]
	}
}

func (r *ringBuf) snapshot() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]byte, len(r.data))
	copy(out, r.data)
	return out
}
