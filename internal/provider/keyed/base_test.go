package keyed

import (
	"reflect"
	"testing"
)

func TestBaseCandidates(t *testing.T) {
	for _, c := range []struct {
		in   string
		want []string
	}{
		{"https://api.example.com", []string{"https://api.example.com", "https://api.example.com/v1", "https://api.example.com/api/v1", "https://api.example.com/api"}},
		{"https://api.example.com/", []string{"https://api.example.com", "https://api.example.com/v1", "https://api.example.com/api/v1", "https://api.example.com/api"}},
		{"  http://host:11434/v1/  ", []string{"http://host:11434/v1", "http://host:11434", "http://host:11434/api/v1", "http://host:11434/api"}},
		{"https://openrouter.ai/api/v1", []string{"https://openrouter.ai/api/v1", "https://openrouter.ai", "https://openrouter.ai/v1", "https://openrouter.ai/api"}},
		{"https://h.example/v1/chat/completions", []string{"https://h.example/v1", "https://h.example", "https://h.example/api/v1", "https://h.example/api"}},
		{"https://h.example/openai/models?x=1", []string{"https://h.example/openai", "https://h.example/openai/v1", "https://h.example/openai/api/v1", "https://h.example/openai/api"}},
		{"api.example.com", nil},
		{"ftp://x", nil},
		{"", nil},
	} {
		if got := BaseCandidates(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q\n got  %v\n want %v", c.in, got, c.want)
		}
	}
}
