package httputil

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// The deadline covers reading the request only: a handler that has read its body and then works for longer than the
// deadline is not cancelled and can still answer.
func TestReadDeadlineDoesNotCutAResponseThatTakesLonger(t *testing.T) {
	old := SmallBodyTimeout
	SmallBodyTimeout = 200 * time.Millisecond
	t.Cleanup(func() { SmallBodyTimeout = old })
	done := make(chan string, 1)
	ts := httptest.NewServer(ReadDeadline(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		select {
		case <-r.Context().Done():
			done <- "cancelled"
		case <-time.After(3 * SmallBodyTimeout):
			_, _ = io.WriteString(w, "late but whole")
			done <- "answered"
		}
	})))
	defer ts.Close()
	resp, err := http.Post(ts.URL, "text/plain", strings.NewReader("small"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if got := <-done; got != "answered" || string(b) != "late but whole" {
		t.Fatalf("%s %q", got, b)
	}
}

func TestIsTimeout(t *testing.T) {
	if IsTimeout(nil) || IsTimeout(io.EOF) {
		t.Fatal("not a timeout")
	}
	if !IsTimeout(fmt.Errorf("reading the form: %w", os.ErrDeadlineExceeded)) {
		t.Fatal("a wrapped deadline error is a timeout")
	}
}
