package provider

import "testing"

func TestLooksFrontier(t *testing.T) {
	for m, want := range map[string]bool{
		"grok-4.7-reasoning": true, "grok-4": false, "grok-mini": false, "grok-4-fast-non-reasoning": false, "grok-4-fast-reasoning": false,
		"claude-opus-4-1": true, "claude-sonnet-4": false, "claude-3-5-haiku": false,
		"o3": true, "o1-pro": true, "o3-mini": false, "o4-mini-high": false, "gpt-5-pro": true, "gpt-4o": false, "gpt-4.1-nano": false,
		"gemini-2.5-pro": true, "gemini-2.5-flash": false, "deepseek-r1": true, "deepseek-chat": false, "qwen3-max": true, "qwq-thinking": true,
		"llama-3.1-8b-instant": false, "": false,
	} {
		if got := LooksFrontier(m); got != want {
			t.Errorf("LooksFrontier(%q) = %v, want %v", m, got, want)
		}
	}
}

func TestParseTimeout(t *testing.T) {
	for in, want := range map[string]int{"120": 120, " 600 ": 600, "1": 1, "86400": 86400} {
		if n, err := ParseTimeout(in); err != nil || n != want {
			t.Errorf("%q: %d %v", in, n, err)
		}
	}
	for _, in := range []string{"", "0", "-5", "1.5", "abc", "86401", "10s"} {
		if _, err := ParseTimeout(in); err == nil {
			t.Errorf("%q must be refused", in)
		}
	}
}
