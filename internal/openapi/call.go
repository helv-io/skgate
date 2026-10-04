package openapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Credential kinds. Credentials are static: skgate sends them with every call and never asks a model for them.
const (
	AuthNone   = "none"
	AuthBearer = "bearer"
	AuthHeader = "header"
	AuthQuery  = "query"
	AuthBasic  = "basic"
)

// Auth is the credential of an API. Name is the header or query parameter name, or the user name for basic.
type Auth struct{ Kind, Name, Value string }

// Limits of a call.
const (
	MaxReadBytes   = 2 << 20 // read from the API at most this much
	MaxResultBytes = 40000   // return at most this much text to the model
	CallTimeout    = 60 * time.Second
	maxRedirects   = 5
)

// Result is what a tool call returns.
type Result struct {
	Text    string
	IsError bool
	// Status is the HTTP status of the answer; 0 when there was none (the request did not get through).
	Status int
}

// Caller makes the HTTP calls of one API.
type Caller struct {
	Base   string
	Auth   Auth
	Client *http.Client // nil: a client with the usual limits
	UA     string
}

func (c *Caller) client() *http.Client {
	if c.Client != nil {
		return c.Client
	}
	return &http.Client{Timeout: CallTimeout, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= maxRedirects {
			return errors.New("too many redirects")
		}
		return sameSite(req, via)
	}}
}

// sameSite lets a redirect through only when it stays on the same host and does not step down from https to http:
// credentials never follow a redirect anywhere else.
func sameSite(req *http.Request, via []*http.Request) error {
	first := via[0].URL
	if req.URL.Host != first.Host || (first.Scheme == "https" && req.URL.Scheme != "https") {
		return http.ErrUseLastResponse
	}
	return nil
}

// Call performs the operation of tool with the arguments a model gave.
func (c *Caller) Call(ctx context.Context, t Tool, args map[string]any) Result {
	req, err := c.build(ctx, t, args)
	if err != nil {
		return Result{Text: err.Error(), IsError: true}
	}
	resp, err := c.client().Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) { // the URL may carry a credential: only the cause is told
			err = ue.Err
		}
		return Result{Text: "the request failed: " + clip(err.Error(), 300), IsError: true}
	}
	defer resp.Body.Close()
	return render(resp)
}

func (c *Caller) build(ctx context.Context, t Tool, args map[string]any) (*http.Request, error) {
	o := t.Op
	path := o.Path
	q := url.Values{}
	hdr := http.Header{}
	for _, p := range o.Params {
		v, ok := args[p.Arg]
		if !ok || v == nil {
			if p.Required {
				return nil, fmt.Errorf("missing required argument %q", p.Arg)
			}
			continue
		}
		switch p.In {
		case "path":
			s, err := scalar(v)
			if err != nil {
				return nil, fmt.Errorf("argument %q: %v", p.Arg, err)
			}
			if s == "" || s == "." || s == ".." {
				return nil, fmt.Errorf("argument %q: %q is not a valid path segment", p.Arg, s)
			}
			path = strings.ReplaceAll(path, "{"+p.Name+"}", url.PathEscape(s))
		case "query":
			addQuery(q, p, v)
		case "header":
			s, err := scalar(v)
			if err != nil {
				return nil, fmt.Errorf("argument %q: %v", p.Arg, err)
			}
			hdr.Set(p.Name, s)
		}
	}
	if strings.Contains(path, "{") && pathVarRE.MatchString(path) {
		return nil, errors.New("the path still has a parameter that was not given")
	}
	base, err := url.Parse(strings.TrimRight(c.Base, "/"))
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" {
		return nil, errors.New("the base URL is not a valid http(s) address")
	}
	for k, vs := range base.Query() { // a query in the base URL (an api-version, say) is kept; the model's arguments win
		if _, has := q[k]; !has {
			q[k] = vs
		}
	}
	raw := base.EscapedPath() + path
	dec, err := url.PathUnescape(raw)
	if err != nil {
		return nil, errors.New("the path cannot be built")
	}
	base.RawPath, base.Path = raw, dec

	var body io.Reader
	ctype := ""
	if o.Body != nil {
		if bv, ok := args["body"]; ok && bv != nil {
			switch o.Body.Type {
			case "application/json":
				b, err := json.Marshal(bv)
				if err != nil {
					return nil, errors.New("argument \"body\" is not valid JSON")
				}
				body, ctype = bytes.NewReader(b), "application/json"
			case "application/x-www-form-urlencoded":
				body, ctype = strings.NewReader(formEncode(bv)), "application/x-www-form-urlencoded"
			default:
				s, _ := scalar(bv)
				body, ctype = strings.NewReader(s), "text/plain; charset=utf-8"
			}
		} else if o.Body.Required {
			return nil, errors.New("missing required argument \"body\"")
		}
	}
	// The credential goes on last: nothing a model sends can replace it.
	switch c.Auth.Kind {
	case AuthQuery:
		q.Set(c.Auth.Name, c.Auth.Value)
	}
	base.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, o.Method, base.String(), body)
	if err != nil {
		return nil, errors.New("the request cannot be built")
	}
	for k, vs := range hdr {
		for _, v := range vs {
			if strings.ContainsAny(v, "\r\n") {
				return nil, fmt.Errorf("header %s has a line break", k)
			}
			req.Header.Add(k, v)
		}
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	req.Header.Set("Accept", "application/json, text/plain;q=0.9, */*;q=0.1")
	ua := c.UA
	if ua == "" {
		ua = "skgate-openapi"
	}
	req.Header.Set("User-Agent", ua)
	switch c.Auth.Kind {
	case AuthBearer:
		req.Header.Set("Authorization", "Bearer "+c.Auth.Value)
	case AuthHeader:
		req.Header.Set(c.Auth.Name, c.Auth.Value)
	case AuthBasic:
		req.SetBasicAuth(c.Auth.Name, c.Auth.Value)
	}
	return req, nil
}

func scalar(v any) (string, error) {
	switch x := v.(type) {
	case string:
		return x, nil
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64), nil
	case bool:
		return strconv.FormatBool(x), nil
	case json.Number:
		return x.String(), nil
	}
	return "", errors.New("expected a text, number or boolean")
}

// addQuery adds a query argument. Arrays repeat the name (or join with commas when explode is false); objects
// become name[key] pairs for the deepObject style, else key pairs; anything else is JSON text.
func addQuery(q url.Values, p Param, v any) {
	explode := p.Explode == nil || *p.Explode
	switch x := v.(type) {
	case []any:
		var items []string
		for _, e := range x {
			s, err := scalar(e)
			if err != nil {
				b, _ := json.Marshal(e)
				s = string(b)
			}
			items = append(items, s)
		}
		if explode {
			for _, s := range items {
				q.Add(p.Name, s)
			}
		} else {
			q.Set(p.Name, strings.Join(items, ","))
		}
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			s, err := scalar(x[k])
			if err != nil {
				b, _ := json.Marshal(x[k])
				s = string(b)
			}
			switch {
			case p.Style == "deepObject":
				q.Add(p.Name+"["+k+"]", s)
			case explode:
				q.Add(k, s)
			default:
				q.Add(p.Name, k+","+s)
			}
		}
	default:
		s, err := scalar(v)
		if err != nil {
			b, _ := json.Marshal(v)
			s = string(b)
		}
		q.Set(p.Name, s)
	}
}

func formEncode(v any) string {
	m, ok := v.(map[string]any)
	if !ok {
		s, _ := scalar(v)
		return s
	}
	q := url.Values{}
	for k, e := range m {
		s, err := scalar(e)
		if err != nil {
			b, _ := json.Marshal(e)
			s = string(b)
		}
		q.Set(k, s)
	}
	return q.Encode()
}

// render turns a response into the text a model reads: the status, then the body, cut at MaxResultBytes.
func render(resp *http.Response) Result {
	ct := resp.Header.Get("Content-Type")
	mt, _, _ := mime.ParseMediaType(ct)
	var sb strings.Builder
	fmt.Fprintf(&sb, "HTTP %d %s", resp.StatusCode, http.StatusText(resp.StatusCode))
	res := Result{IsError: resp.StatusCode >= 400, Status: resp.StatusCode}
	if binaryType(mt) {
		n, _ := io.Copy(io.Discard, io.LimitReader(resp.Body, MaxReadBytes))
		fmt.Fprintf(&sb, "\n[binary response, %s, %d bytes, not shown]", mt, n)
		res.Text = sb.String()
		return res
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, MaxReadBytes+1))
	total := len(b)
	capped := total > MaxReadBytes
	if capped {
		b = b[:MaxReadBytes]
	}
	if len(b) == 0 {
		res.Text = sb.String() + "\n[empty body]"
		return res
	}
	if mt != "" {
		sb.WriteString("\nContent-Type: " + mt)
	}
	sb.WriteString("\n\n")
	shown := b
	if len(shown) > MaxResultBytes {
		shown = shown[:MaxResultBytes]
		for i := 0; i < utf8.UTFMax-1 && len(shown) > 0; i++ { // do not end in half a character
			if r, n := utf8.DecodeLastRune(shown); r != utf8.RuneError || n != 1 {
				break
			}
			shown = shown[:len(shown)-1]
		}
	}
	sb.Write(bytes.ToValidUTF8(shown, []byte("?")))
	if len(b) > len(shown) || capped {
		size := strconv.Itoa(total)
		if capped {
			size = "more than " + strconv.Itoa(MaxReadBytes)
		}
		fmt.Fprintf(&sb, "\n[truncated: showing the first %d of %s bytes; ask for less (filters, a limit, fewer fields)]", len(shown), size)
	}
	res.Text = sb.String()
	return res
}

func binaryType(mt string) bool {
	switch {
	case mt == "":
		return false
	case strings.HasPrefix(mt, "image/"), strings.HasPrefix(mt, "audio/"), strings.HasPrefix(mt, "video/"), strings.HasPrefix(mt, "font/"):
		return true
	}
	switch mt {
	case "application/octet-stream", "application/pdf", "application/zip", "application/gzip", "application/x-tar":
		return true
	}
	return false
}
