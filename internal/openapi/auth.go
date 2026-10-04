package openapi

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"time"
)

// Scheme is the way a description says a key is sent. Kind is "" when it says nothing usable.
type Scheme struct {
	Kind string // bearer, basic, header or query
	Name string // the header or query parameter, for header and query
}

// Way names how a key is sent: bearer, basic, token (Authorization: Token <key>), header:<Name> or query:<name>.
type Way string

// Way turns a scheme into the way to send the key; a description that says nothing means bearer.
func (s Scheme) Way() Way {
	switch s.Kind {
	case AuthBasic:
		return "basic"
	case AuthHeader:
		return Way("header:" + s.Name)
	case AuthQuery:
		return Way("query:" + s.Name)
	}
	return "bearer"
}

// Auth builds the credential that sends value this way. Basic reads user:password.
func (w Way) Auth(value string) Auth {
	switch {
	case w == "basic":
		user, pass, _ := strings.Cut(value, ":")
		return Auth{Kind: AuthBasic, Name: user, Value: pass}
	case w == "token":
		return Auth{Kind: AuthHeader, Name: "Authorization", Value: "Token " + value}
	case strings.HasPrefix(string(w), "header:"):
		return Auth{Kind: AuthHeader, Name: string(w)[7:], Value: value}
	case strings.HasPrefix(string(w), "query:"):
		return Auth{Kind: AuthQuery, Name: string(w)[6:], Value: value}
	}
	return Auth{Kind: AuthBearer, Value: value}
}

// Valid reports whether w is a way this package makes.
func (w Way) Valid() bool {
	switch {
	case w == "bearer", w == "basic", w == "token":
		return true
	case strings.HasPrefix(string(w), "header:"):
		return len(w) > 7
	case strings.HasPrefix(string(w), "query:"):
		return len(w) > 6
	}
	return false
}

// rank orders the ways a description offers when it lists several: a bearer token, a key in a header, basic, a
// key in the query string last.
func rank(kind string) int {
	switch kind {
	case AuthBearer:
		return 0
	case AuthHeader:
		return 1
	case AuthBasic:
		return 2
	case AuthQuery:
		return 3
	}
	return 9
}

// Scheme reads how the description wants a key sent: from the security requirements of the API, else from the
// security schemes it defines. Swagger 2 files are read the same way once converted.
func (d *Doc) Scheme() Scheme {
	schemes := obj(obj(d.Raw["components"])["securitySchemes"])
	var names []string
	if sec, has := d.Raw["security"]; has {
		names = requirementNames(list(sec))
	} else {
		paths := obj(d.Raw["paths"])
		for _, p := range sortedKeys(paths) {
			for _, m := range sortedKeys(obj(paths[p])) {
				if isMethod(m) {
					names = append(names, requirementNames(list(obj(obj(paths[p])[m])["security"]))...)
				}
			}
		}
	}
	if len(names) == 0 {
		names = sortedKeys(schemes) // defined but never required: still the best hint there is
	}
	best := Scheme{}
	for _, n := range names {
		if s := schemeOf(obj(schemes[n])); s.Kind != "" && (best.Kind == "" || rank(s.Kind) < rank(best.Kind)) {
			best = s
		}
	}
	return best
}

func requirementNames(reqs []any) []string {
	var out []string
	for _, r := range reqs {
		m := obj(r)
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out = append(out, keys...)
	}
	return out
}

func schemeOf(m map[string]any) Scheme {
	switch strings.ToLower(str(m["type"])) {
	case "http":
		switch strings.ToLower(str(m["scheme"])) {
		case "bearer":
			return Scheme{Kind: AuthBearer}
		case "basic":
			return Scheme{Kind: AuthBasic}
		}
	case "oauth2", "openidconnect":
		return Scheme{Kind: AuthBearer} // the token is pasted; the flow to get one is not run
	case "apikey":
		name := strings.TrimSpace(str(m["name"]))
		if name == "" {
			return Scheme{}
		}
		switch strings.ToLower(str(m["in"])) {
		case "header":
			return Scheme{Kind: AuthHeader, Name: name}
		case "query":
			return Scheme{Kind: AuthQuery, Name: name}
		}
	}
	return Scheme{}
}

// needsAuth reports whether the description marks the operation as requiring a credential.
func (d *Doc) needsAuth(o Op) bool {
	op := obj(obj(obj(d.Raw["paths"])[o.Path])[strings.ToLower(o.Method)])
	sec, has := op["security"]
	if !has {
		sec = d.Raw["security"]
	}
	for _, r := range list(sec) {
		if len(obj(r)) > 0 {
			return true
		}
	}
	return false
}

// ProbeOp picks the operation to ask when a key is tried: a GET that needs no arguments, preferably one the
// description marks as requiring a credential. Only reading operations are ever used.
func (d *Doc) ProbeOp(ops []Op) (Op, bool) {
	var first *Op
	for i := range ops {
		o := ops[i]
		if o.Method != http.MethodGet || o.Skip != "" || o.Body != nil || strings.Contains(o.Path, "{") {
			continue
		}
		needs := false
		for _, p := range o.Params {
			needs = needs || p.Required
		}
		if needs {
			continue
		}
		if d.needsAuth(o) {
			return o, true
		}
		if first == nil {
			first = &ops[i]
		}
	}
	if first != nil {
		return *first, true
	}
	return Op{}, false
}

// Ways lists the ways to try for value, in order. The way the description names is the only one when it names one;
// when it is silent the key is tried as a bearer token first, then the common alternatives. A key in the query
// string is never a guess.
func Ways(s Scheme, value string) []Way {
	if s.Kind != "" {
		return []Way{s.Way()}
	}
	ways := []Way{"bearer", "header:X-API-Key", "header:Api-Key", "token"}
	if i := strings.Index(value, ":"); i > 0 && i < len(value)-1 {
		ways = append(ways, "basic")
	}
	return ways
}

// keyProbeTimeout bounds one request of a key probe.
const keyProbeTimeout = 10 * time.Second

// Tried is the outcome of trying a key.
type Tried struct {
	Way    Way
	Status int  // the status of the last answer, 0 when none came
	OK     bool // some way was not refused
}

// TryKey asks base with GET, one way after the other, until an answer is not 401 or 403. Only the server base and
// only the operation given are used. A request that gets no answer ends the search with Status 0.
func TryKey(ctx context.Context, base string, op Op, value string, ways []Way, ua string) Tried {
	var last Tried
	for _, w := range ways {
		c := &Caller{Base: base, Auth: w.Auth(value), UA: ua, Client: &http.Client{Timeout: keyProbeTimeout, CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return http.ErrUseLastResponse
			}
			return sameSite(req, via)
		}}}
		res := c.Call(ctx, Tool{Op: op}, nil)
		last = Tried{Way: w, Status: res.Status}
		if res.Status == 0 {
			return last
		}
		if res.Status != http.StatusUnauthorized && res.Status != http.StatusForbidden {
			last.OK = true
			return last
		}
	}
	return last
}
