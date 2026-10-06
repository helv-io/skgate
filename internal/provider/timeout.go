package provider

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// DefaultHelperTimeout is how long the MCP helper model may stay silent before a suggestion is given up.
const DefaultHelperTimeout = 300 * time.Second

// FrontierTimeoutSecs is the timeout suggested (never applied) for models that look like heavy reasoners.
const FrontierTimeoutSecs = 600

// MaxHelperTimeoutSecs is the largest timeout accepted (a day), only there to catch typos.
const MaxHelperTimeoutSecs = 86400

// HelperTimeout is the longest the MCP helper model may stay silent; the stored setting, else the default.
func (s Settings) HelperTimeout(id string) time.Duration {
	if v, ok := s.Get(id, "timeout"); ok {
		if n, err := ParseTimeout(v); err == nil {
			return time.Duration(n) * time.Second
		}
	}
	return DefaultHelperTimeout
}

// SetHelperTimeout stores the helper model's timeout in seconds.
func (s Settings) SetHelperTimeout(id string, secs int) error {
	return s.Set(id, "timeout", strconv.Itoa(secs))
}

// ParseTimeout reads a whole number of seconds, 1 to MaxHelperTimeoutSecs.
func ParseTimeout(v string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n < 1 || n > MaxHelperTimeoutSecs {
		return 0, errors.New("the timeout is a whole number of seconds, 1 to " + strconv.Itoa(MaxHelperTimeoutSecs))
	}
	return n, nil
}

var (
	// heavy: names that announce a large or reasoning-first model, whatever the provider.
	heavyModel = regexp.MustCompile(`(^|[^a-z0-9])(opus|pro|ultra|max|heavy|reasoning|reasoner|thinking|think|deep|r1|o[1-9])([^a-z0-9]|$)`)
	// light: names that announce a small or fast one; these win over heavy words (grok-4-fast-non-reasoning, o3-mini).
	lightModel = regexp.MustCompile(`(^|[^a-z0-9])(mini|nano|micro|lite|flash|fast|haiku|instant|small|non|turbo|chat)([^a-z0-9]|$)`)
)

// LooksFrontier guesses from the name alone whether a model is a heavy reasoning model, which takes long
// enough that a longer timeout is worth suggesting. It is a hint, never a setting: the user decides.
func LooksFrontier(model string) bool {
	m := strings.ToLower(model)
	return heavyModel.MatchString(m) && !lightModel.MatchString(m)
}
