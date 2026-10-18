// Package directory is `skgate stdio`: a tiny read-only MCP server on stdin and stdout.
//
// It exists so MCP directories and scanners, which start a server and ask what it offers, can read
// about skgate without OIDC settings, a listening port, a database or a network. It is not the
// gateway: the real thing is the HTTP server, and nothing here reads the environment or the disk.
package directory

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/helv-io/skgate/internal/config"
)

// protocols are the MCP versions this server answers with, newest first. A client that asks for
// another one gets the newest and decides whether to continue.
var protocols = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

const (
	repoURL = "https://github.com/helv-io/skgate"
	docsURL = repoURL + "/tree/master/docs"
	// Summary is the one wording used for skgate everywhere (AGENT.md).
	summary = "An OAuth-protected MCP gateway that can also run the servers. It also proxies SI providers behind one OpenAI-compatible address with model aliases, and a Grok subscription becomes that API with no API key."
)

// JSON-RPC error codes.
const (
	errParse          = -32700
	errInvalidRequest = -32600
	errMethod         = -32601
	errParams         = -32602
)

// maxLine caps one incoming message. The tools take a few short strings, so this is generous.
const maxLine = 1 << 20

type message struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

func (m message) hasID() bool { return len(m.ID) > 0 && string(m.ID) != "null" }

// Run reads one JSON-RPC message per line from in and writes one answer per line to out, until in
// ends. It never writes anything but protocol messages to out.
func Run(in io.Reader, out io.Writer) error {
	r := bufio.NewReaderSize(in, 64<<10)
	for {
		line, err := readLine(r)
		if len(bytes.TrimSpace(line)) > 0 {
			if ans := Handle(line); ans != nil {
				if _, werr := out.Write(append(ans, '\n')); werr != nil {
					return werr
				}
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

// readLine returns one line, or io.EOF with whatever came before the end. A line over maxLine is
// dropped, and its answer is a parse error.
func readLine(r *bufio.Reader) ([]byte, error) {
	var buf []byte
	for {
		part, isPrefix, err := r.ReadLine()
		if len(buf)+len(part) <= maxLine {
			buf = append(buf, part...)
		} else {
			buf = []byte("{")
		}
		if err != nil || !isPrefix {
			return buf, err
		}
	}
}

// Handle answers one JSON-RPC message. It returns nil for a notification.
func Handle(raw []byte) []byte {
	var m message
	if err := json.Unmarshal(raw, &m); err != nil {
		return fail(nil, errParse, "invalid JSON")
	}
	if m.Method == "" || !m.hasID() { // a notification, or a response to a request this server never sends
		return nil
	}
	switch m.Method {
	case "initialize":
		return result(m.ID, initialize(m.Params))
	case "ping":
		return result(m.ID, map[string]any{})
	case "tools/list":
		return result(m.ID, map[string]any{"tools": toolList()})
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(m.Params, &p); err != nil || p.Name == "" {
			return fail(m.ID, errParams, "tools/call needs a tool name")
		}
		res, ok := call(p.Name, p.Arguments)
		if !ok {
			return fail(m.ID, errParams, "unknown tool: "+clip(p.Name))
		}
		return result(m.ID, res)
	default:
		return fail(m.ID, errMethod, "method not supported: "+clip(m.Method))
	}
}

func initialize(params json.RawMessage) map[string]any {
	var p struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	_ = json.Unmarshal(params, &p)
	proto := protocols[0]
	for _, v := range protocols {
		if v == p.ProtocolVersion {
			proto = v
		}
	}
	return map[string]any{
		"protocolVersion": proto,
		"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
		"serverInfo":      map[string]any{"name": "skgate", "title": "skgate (directory mode)", "version": config.Version},
		"instructions":    "This is skgate's read-only directory mode, started with `skgate stdio`. It only describes skgate: three tools return its summary, its features and its settings. It does not proxy, sign in or store anything. To use skgate itself, run its Docker image and connect an MCP client to its /mcp address. Setup is at " + repoURL + ".",
	}
}

func result(id json.RawMessage, res any) []byte {
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": res})
	return b
}

func fail(id json.RawMessage, code int, msg string) []byte {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": msg}})
	return b
}

func clip(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < ' ' {
			return ' '
		}
		return r
	}, s)
	if len(s) > 60 {
		return s[:60]
	}
	return s
}
