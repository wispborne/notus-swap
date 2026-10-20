package logbuf

import "testing"

func TestKeepsNewestWholeLines(t *testing.T) {
	b := New(12)
	b.Write([]byte("first line\n"))
	b.Write([]byte("second\n"))
	hist, writes, cancel := b.Subscribe()
	defer cancel()
	// 18 bytes is over the 12 kept, so "first line\n" goes as a whole.
	if string(hist) != "second\n" {
		t.Errorf("history %q, want %q", hist, "second\n")
	}
	b.Write([]byte("third\n"))
	if got := string(<-writes); got != "third\n" {
		t.Errorf("subscriber got %q", got)
	}
}

func TestBytes(t *testing.T) {
	b := New(100)
	b.Write([]byte("one\ntwo\n"))
	if got := string(b.Bytes()); got != "one\ntwo\n" {
		t.Errorf("got %q", got)
	}
}
