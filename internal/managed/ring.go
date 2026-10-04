package managed

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Limits of one process's log: the last DefaultLogLines lines or MaxLogBytes of text, whichever is smaller, and
// a line is cut at maxLineBytes. Only the current and the previous run are kept.
const (
	DefaultLogLines = 1000
	MaxLogBytes     = 512 << 10
	maxLineBytes    = 4000
)

// Line is one captured output line. Src is "out" (unparseable stdout), "err" (stderr), "git",
// "install" or "sys" (skgate's own notes about the process: start, stop, exit).
type Line struct {
	ID    uint64    `json:"id"`  // grows by one per line for the life of the log; live views resume from it
	Run   int       `json:"run"` // which start of the process the line belongs to
	T     time.Time `json:"t"`
	Src   string    `json:"src"`
	Text  string    `json:"text"`
	Level string    `json:"lvl,omitempty"`   // error, warn, info or debug when the line says so
	Trunc bool      `json:"trunc,omitempty"` // the line was cut at maxLineBytes
	Start bool      `json:"start,omitempty"` // the first line of a run
}

// Ring is the bounded log of a process: lines and bytes are capped, and a run older than the previous one is dropped.
type Ring struct {
	mu       sync.Mutex
	lines    []Line
	bytes    int
	max      int
	maxBytes int
	next     uint64 // ID of the next line
	run      int
	gen      int           // grows when the log is cleared, so a live view knows to start over
	wake     chan struct{} // closed on the next change, only while somebody waits
}

// NewRing returns a ring holding at most max lines (and MaxLogBytes of text).
func NewRing(max int) *Ring {
	if max < 1 {
		max = 1
	}
	return &Ring{max: max, maxBytes: MaxLogBytes, next: 1}
}

// clipLineT cuts a line at maxLineBytes and reports whether it did.
func clipLineT(s string) (string, bool) {
	s = strings.TrimRight(s, "\r\n")
	if len(s) <= maxLineBytes {
		return s, false
	}
	s = s[:maxLineBytes]
	for !utf8.ValidString(s) && len(s) > 0 {
		s = s[:len(s)-1]
	}
	return s + "…", true
}

func clipLine(s string) string { s, _ = clipLineT(s); return s }

var (
	levelKeyRE = regexp.MustCompile(`(?i)\blevel["']?\s*[:=]\s*["']?([a-z]+)`)
	levelTagRE = regexp.MustCompile(`(?i)(?:^|[\s\[\(|"'=:<])(fatal|panic|critical|crit|error|err|warning|warn|info|inf|debug|dbg|trace)(?:$|[\s\]\)|"'=:,>])`)
)

// detectLevel reads a log level from a line: a level=… or "level":"…" field anywhere, or a bare tag
// (ERROR, [warn], INFO:) near the start. It returns "" when the line does not say.
func detectLevel(s string) string {
	if len(s) > 400 {
		s = s[:400]
	}
	m := levelKeyRE.FindStringSubmatch(s)
	if m == nil {
		head := s
		if len(head) > 48 {
			head = head[:48]
		}
		m = levelTagRE.FindStringSubmatch(head)
	}
	if m == nil {
		return ""
	}
	switch strings.ToLower(m[1]) {
	case "fatal", "panic", "critical", "crit", "error", "err":
		return "error"
	case "warning", "warn":
		return "warn"
	case "info", "inf":
		return "info"
	case "debug", "dbg", "trace":
		return "debug"
	}
	return ""
}

func (r *Ring) dropFront(n int) {
	for i := 0; i < n; i++ {
		r.bytes -= len(r.lines[i].Text)
	}
	r.lines = append(r.lines[:0:0], r.lines[n:]...)
}

// trimLocked drops the oldest lines beyond the line and byte caps.
func (r *Ring) trimLocked() {
	n := 0
	bytes := r.bytes
	for n < len(r.lines)-1 && (len(r.lines)-n > r.max || bytes > r.maxBytes) {
		bytes -= len(r.lines[n].Text)
		n++
	}
	if n > 0 {
		r.dropFront(n)
	}
}

func (r *Ring) changedLocked() {
	if r.wake != nil {
		close(r.wake)
		r.wake = nil
	}
}

func (r *Ring) addLocked(l Line) Line {
	l.ID, l.Run = r.next, r.run
	r.next++
	r.lines = append(r.lines, l)
	r.bytes += len(l.Text)
	if len(r.lines) > r.max || r.bytes > r.maxBytes {
		// trim in chunks, so a full log does not copy itself on every line
		if len(r.lines) > r.max+r.max/8+8 || r.bytes > r.maxBytes {
			r.trimLocked()
		}
	}
	r.changedLocked()
	return l
}

// Add appends a line, dropping the oldest when the log is full, and returns it.
func (r *Ring) Add(src, text string) Line {
	text, cut := clipLineT(text)
	l := Line{T: time.Now(), Src: src, Text: text, Trunc: cut}
	if src != "sys" {
		l.Level = detectLevel(text)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.addLocked(l)
}

// BeginRun starts a new run with a first line, and drops the runs before the previous one.
func (r *Ring) BeginRun(text string) Line {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.run++
	n := 0
	for n < len(r.lines) && r.lines[n].Run < r.run-1 {
		n++
	}
	if n > 0 {
		r.dropFront(n)
	}
	return r.addLocked(Line{T: time.Now(), Src: "sys", Text: text, Start: true})
}

// window returns the lines to show: those that fit the caps, oldest first.
func (r *Ring) window() []Line {
	l := r.lines
	if len(l) > r.max {
		l = l[len(l)-r.max:]
	}
	return l
}

// Last returns up to n most recent lines, oldest first (n <= 0 means all).
func (r *Ring) Last(n int) []Line {
	r.mu.Lock()
	defer r.mu.Unlock()
	w := r.window()
	if n <= 0 || n > len(w) {
		n = len(w)
	}
	return append([]Line(nil), w[len(w)-n:]...)
}

// Since returns the lines after the line with ID after. reset is true when the log was cleared or restarted
// since (gen differs from the caller's, or after is beyond the newest line): the caller should drop what it
// shows and the lines returned are the whole log.
func (r *Ring) Since(after uint64, gen int) (lines []Line, newGen int, reset bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	w := r.window()
	if gen != r.gen || after >= r.next {
		return append([]Line(nil), w...), r.gen, true
	}
	i := sort.Search(len(w), func(i int) bool { return w[i].ID > after })
	return append([]Line(nil), w[i:]...), r.gen, false
}

// Gen is the clear counter; see Since.
func (r *Ring) Gen() int { r.mu.Lock(); defer r.mu.Unlock(); return r.gen }

// Wait returns a channel that closes when the log next changes.
func (r *Ring) Wait() <-chan struct{} {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.wake == nil {
		r.wake = make(chan struct{})
	}
	return r.wake
}

// Len is the number of lines held.
func (r *Ring) Len() int { r.mu.Lock(); defer r.mu.Unlock(); return len(r.window()) }

// Clear drops all lines.
func (r *Ring) Clear() {
	r.mu.Lock()
	r.lines, r.bytes = nil, 0
	r.gen++
	r.changedLocked()
	r.mu.Unlock()
}

type ringDoc struct {
	Next  uint64 `json:"next"`
	Run   int    `json:"run"`
	Lines []Line `json:"lines"`
}

// Export serializes the log for the file kept between runs.
func (r *Ring) Export() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	b, _ := json.Marshal(ringDoc{Next: r.next, Run: r.run, Lines: r.window()})
	return b
}

// Import restores what Export wrote (lines older than the previous run are left out), keeping the IDs.
func (r *Ring) Import(b []byte) error {
	var d ringDoc
	if err := json.Unmarshal(b, &d); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lines, r.bytes, r.run, r.next = nil, 0, d.Run, d.Next
	for _, l := range d.Lines {
		if l.Run < d.Run-1 {
			continue
		}
		l.Text, _ = clipLineT(l.Text)
		r.lines = append(r.lines, l)
		r.bytes += len(l.Text)
		if l.ID >= r.next {
			r.next = l.ID + 1
		}
	}
	r.trimLocked()
	r.gen++
	r.changedLocked()
	return nil
}

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
