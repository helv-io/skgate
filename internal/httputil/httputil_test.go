package httputil

import "testing"

func TestMask(t *testing.T) {
	cases := map[string]string{
		"":                 "",
		"a":                "************",
		"1234567":          "************", // shorter than 8: asterisks only
		"12345678":         "************5678",
		"sk-abcdefghijklm": "************jklm",
	}
	for in, want := range cases {
		got := Mask(in)
		if got != want {
			t.Errorf("Mask(%q) = %q, want %q", in, got, want)
		}
		if len(in) >= 8 && len(got) > len(MaskStars)+4 {
			t.Errorf("Mask(%q) shows too much: %q", in, got)
		}
	}
}
