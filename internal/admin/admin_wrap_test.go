package admin

import "testing"

func TestWrapURLBreaksAtSegments(t *testing.T) {
	got := string(wrapURL("https://h.example", "/mcp/", "a<b"))
	want := "https:/<wbr>/<wbr>h.example/<wbr>mcp/<wbr>a&lt;b"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
