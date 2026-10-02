package managed

import (
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// maxLineBytes clips one log line, so the buffer is bounded by lines * maxLineBytes.
const maxLineBytes = 4000

// Line is one captured output line. Src is "out" (unparseable stdout), "err" (stderr), "git",
// "install" or "sys" (skgate's own notes about the process).
type Line struct {
	T    time.Time
	Src  string
	Text string
}

// Ring is a fixed-size buffer of the most recent output lines of a process.
type Ring struct {
	mu   sync.Mutex
	buf  []Line
	head int // index of the oldest line
	n    int
}

// NewRing returns a ring holding at most max lines.
func NewRing(max int) *Ring {
	if max < 1 {
		max = 1
	}
	return &Ring{buf: make([]Line, max)}
}

func clipLine(s string) string {
	s = strings.TrimRight(s, "\r\n")
	if len(s) <= maxLineBytes {
		return s
	}
	s = s[:maxLineBytes]
	for !utf8.ValidString(s) && len(s) > 0 {
		s = s[:len(s)-1]
	}
	return s + "…"
}

// Add appends a line, dropping the oldest when full.
func (r *Ring) Add(src, text string) {
	l := Line{T: time.Now(), Src: src, Text: clipLine(text)}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.n < len(r.buf) {
		r.buf[(r.head+r.n)%len(r.buf)] = l
		r.n++
		return
	}
	r.buf[r.head] = l
	r.head = (r.head + 1) % len(r.buf)
}

// Last returns up to n most recent lines, oldest first (n <= 0 means all).
func (r *Ring) Last(n int) []Line {
	r.mu.Lock()
	defer r.mu.Unlock()
	if n <= 0 || n > r.n {
		n = r.n
	}
	out := make([]Line, 0, n)
	for i := r.n - n; i < r.n; i++ {
		out = append(out, r.buf[(r.head+i)%len(r.buf)])
	}
	return out
}

// Len is the number of lines held.
func (r *Ring) Len() int { r.mu.Lock(); defer r.mu.Unlock(); return r.n }

// Clear drops all lines.
func (r *Ring) Clear() { r.mu.Lock(); r.head, r.n = 0, 0; r.mu.Unlock() }

// lineWriter splits written bytes into lines and hands each to fn. A trailing partial line is
// emitted by Flush. It is safe for the concurrent stdout/stderr use of exec.Cmd only when one
// writer is used per stream.
type lineWriter struct {
	mu  sync.Mutex
	buf []byte
	fn  func(string)
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, p...)
	for {
		i := indexNL(w.buf)
		if i < 0 {
			break
		}
		w.fn(string(w.buf[:i]))
		w.buf = w.buf[i+1:]
	}
	if len(w.buf) > 4*maxLineBytes { // a line without a newline: cut it instead of growing
		w.fn(string(w.buf))
		w.buf = nil
	}
	return len(p), nil
}

// Flush emits a pending partial line.
func (w *lineWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.buf) > 0 {
		w.fn(string(w.buf))
		w.buf = nil
	}
}

func indexNL(b []byte) int {
	for i, c := range b {
		if c == '\n' {
			return i
		}
	}
	return -1
}
