// Package openapi turns an OpenAPI 3.x or Swagger 2 description (JSON, YAML or TOML) into MCP tools, one per
// operation, and makes the HTTP calls itself. It never runs a process.
package openapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	toml "github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"
)

// MaxSpecBytes is the largest description skgate reads.
const MaxSpecBytes = 10 << 20

// Doc is a description normalized to OpenAPI 3 (a Swagger 2 file is converted on import). Raw is plain JSON
// data: maps, slices, strings, float64, bool, nil.
type Doc struct {
	Raw map[string]any
	// Source is "swagger 2" when the file was converted, else "openapi 3".
	Source string
}

// Parse reads a description in JSON, YAML or TOML. It fails only when nothing usable can be made of the text;
// problems that can be repaired are reported by Validate.
func Parse(raw []byte) (*Doc, error) {
	if len(raw) > MaxSpecBytes {
		return nil, fmt.Errorf("the description is larger than %d MB", MaxSpecBytes>>20)
	}
	return ParseStored(raw)
}

// ParseStored reads a description that skgate stored itself (normalized JSON). It has no size limit of its own:
// the limit applies to what a person sends, and a description under it can be a little larger once normalized.
func ParseStored(raw []byte) (*Doc, error) {
	raw = bytes.TrimPrefix(bytes.TrimSpace(raw), []byte("\xef\xbb\xbf"))
	if len(raw) == 0 {
		return nil, errors.New("the description is empty")
	}
	m, err := decode(raw)
	if err != nil {
		return nil, err
	}
	d := &Doc{Raw: m, Source: "openapi 3"}
	switch v := str(m["openapi"]); {
	case strings.HasPrefix(v, "3."):
	case strings.HasPrefix(str(m["swagger"]), "2"):
		d.Raw, d.Source = convertSwagger(m), "swagger 2"
	case v != "":
		return nil, fmt.Errorf("OpenAPI version %q is not supported (3.x and Swagger 2 are)", v)
	default:
		return nil, errors.New("this is not an OpenAPI or Swagger description: it has no \"openapi\" or \"swagger\" field")
	}
	return d, nil
}

// decode tries JSON, then YAML, then TOML.
func decode(raw []byte) (map[string]any, error) {
	var first error
	if raw[0] == '{' {
		var v any
		dec := json.NewDecoder(bytes.NewReader(raw))
		if err := dec.Decode(&v); err == nil {
			if m, ok := v.(map[string]any); ok {
				return m, nil
			}
		} else {
			first = fmt.Errorf("JSON: %v", err)
		}
	}
	var y any
	if err := yaml.Unmarshal(raw, &y); err == nil {
		if m, ok := normalize(y).(map[string]any); ok && len(m) > 0 {
			return m, nil
		}
	} else if first == nil {
		first = fmt.Errorf("YAML: %v", err)
	}
	var t map[string]any
	if err := toml.Unmarshal(raw, &t); err == nil && len(t) > 0 {
		return normalize(t).(map[string]any), nil
	}
	if first != nil {
		return nil, fmt.Errorf("cannot read the description as JSON, YAML or TOML (%s)", first)
	}
	return nil, errors.New("cannot read the description as JSON, YAML or TOML")
}

// normalize turns decoder output into plain JSON data: string keys, float64 numbers, string dates.
func normalize(v any) any {
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			x[k] = normalize(e)
		}
		return x
	case map[any]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[fmt.Sprint(k)] = normalize(e)
		}
		return out
	case []any:
		for i, e := range x {
			x[i] = normalize(e)
		}
		return x
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case uint64:
		return float64(x)
	case float32:
		return float64(x)
	case time.Time:
		return x.Format(time.RFC3339)
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return nil
		}
	}
	return v
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func obj(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func list(v any) []any {
	l, _ := v.([]any)
	return l
}

func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// JSON renders the normalized description, compact.
func (d *Doc) JSON() []byte {
	b, _ := json.Marshal(d.Raw)
	return b
}

// Title is info.title, or "".
func (d *Doc) Title() string { return str(obj(d.Raw["info"])["title"]) }

// Server is a base URL the description offers.
type Server struct{ URL, Description string }

// Servers lists the description's servers with variables replaced by their defaults. A relative URL is resolved
// against specURL when that is known.
func (d *Doc) Servers(specURL string) []Server {
	var out []Server
	for _, s := range list(d.Raw["servers"]) {
		sm := obj(s)
		u := str(sm["url"])
		if u == "" {
			continue
		}
		for name, v := range obj(sm["variables"]) {
			u = strings.ReplaceAll(u, "{"+name+"}", str(obj(v)["default"]))
		}
		out = append(out, Server{URL: resolveServer(u, specURL), Description: str(sm["description"])})
	}
	return out
}

func resolveServer(u, specURL string) string {
	if strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") || specURL == "" {
		return u
	}
	return resolveRef(specURL, u)
}
