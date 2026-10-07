// Package logbuf keeps the last part of notus-swap's own log in memory, so
// the Logs page can show it without journald.
package logbuf

import (
	"bytes"
	"sync"
)

// Buffer is an io.Writer that keeps the newest Size bytes written to it and
// passes new writes on to subscribers.
type Buffer struct {
	size int

	mu   sync.Mutex
	data []byte
	subs map[chan []byte]struct{}
}

func New(size int) *Buffer {
	return &Buffer{size: size, subs: map[chan []byte]struct{}{}}
}

// Write never blocks: a subscriber that falls behind misses writes.
func (b *Buffer) Write(p []byte) (int, error) {
	c := bytes.Clone(p)
	b.mu.Lock()
	defer b.mu.Unlock()
	b.data = append(b.data, c...)
	if over := len(b.data) - b.size; over > 0 {
		// Cut at a line break, so the history starts with a whole line.
		cut := over
		if i := bytes.IndexByte(b.data[over:], '\n'); i >= 0 {
			cut = over + i + 1
		}
		b.data = append([]byte(nil), b.data[cut:]...)
	}
	for ch := range b.subs {
		select {
		case ch <- c:
		default:
		}
	}
	return len(p), nil
}

// Subscribe returns what the buffer holds now and a channel of every later
// write. Taking both in one step means nothing is missed or repeated.
func (b *Buffer) Subscribe() (history []byte, writes <-chan []byte, cancel func()) {
	ch := make(chan []byte, 256)
	b.mu.Lock()
	defer b.mu.Unlock()
	b.subs[ch] = struct{}{}
	return bytes.Clone(b.data), ch, func() {
		b.mu.Lock()
		delete(b.subs, ch)
		b.mu.Unlock()
	}
}
