package provider

import "testing"

func TestParseCallback(t *testing.T) {
	for in, want := range map[string][2]string{
		"http://127.0.0.1:56121/callback?code=abc&state=xyz": {"abc", "xyz"},
		"code=abc&state=xyz": {"abc", "xyz"}, "abc": {"abc", ""}, "": {"", ""},
	} {
		if c, s := ParseCallback(in); c != want[0] || s != want[1] {
			t.Errorf("%q => %q %q", in, c, s)
		}
	}
}
