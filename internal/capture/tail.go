// Package capture keeps the end of a command's output while it streams.
package capture

import "sync"

// Tail is an io.Writer that keeps the last limit bytes written to it. It's safe
// to share between stdout and stderr.
type Tail struct {
	limit int
	mu    sync.Mutex
	buf   []byte
	start int // index of the oldest byte once buf is full
	total int64
}

func NewTail(limit int) *Tail {
	return &Tail{limit: limit}
}

func (t *Tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	n := len(p)
	t.total += int64(n)
	if len(p) >= t.limit {
		t.buf = append(t.buf[:0], p[len(p)-t.limit:]...)
		t.start = 0
		return n, nil
	}
	if room := t.limit - len(t.buf); room > 0 {
		k := min(room, len(p))
		t.buf = append(t.buf, p[:k]...)
		p = p[k:]
	}
	for len(p) > 0 {
		k := copy(t.buf[t.start:], p)
		p = p[k:]
		t.start = (t.start + k) % t.limit
	}
	return n, nil
}

// Bytes returns what's kept, oldest first, and how many bytes were written in
// all.
func (t *Tail) Bytes() ([]byte, int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]byte, 0, len(t.buf))
	out = append(out, t.buf[t.start:]...)
	out = append(out, t.buf[:t.start]...)
	return out, t.total
}
