package mcp

import (
	"context"
	"encoding/json"
	"errors"
)

// LookupTool is one tool of a stored upstream as a client sees it in tools/list: name, description and input
// schema. For OpenAPI it uses the switched-on tools; for every other kind it asks the upstream.
func (s *Server) LookupTool(ctx context.Context, alias, name string) (map[string]any, error) {
	up, ok := s.Upstreams.Get(alias)
	if !ok {
		return nil, errors.New("unknown upstream")
	}
	if up.IsOpenAPI() {
		t, ok := s.OpenAPITool(alias, name)
		if !ok {
			return nil, errors.New("that tool is off, save the tools first")
		}
		return t, nil
	}
	if name == "" {
		return nil, errors.New("that tool is not on the list")
	}
	t, err := s.findUpstreamTool(ctx, up, name)
	if err != nil {
		return nil, err
	}
	if t == nil {
		return nil, errors.New("that tool is not on the list")
	}
	return t, nil
}

// CallTool runs one tool of a stored upstream through the same code a client's tools/call uses (the aggregated
// /mcp and /mcp/{alias} both end here for remote and managed; OpenAPI goes through oaDispatch). It returns the
// text of the answer and whether the tool reported an error; err is set when the call could not be made at all.
func (s *Server) CallTool(ctx context.Context, alias, name string, args map[string]any) (text string, isErr bool, err error) {
	up, ok := s.Upstreams.Get(alias)
	if !ok {
		return "", false, errors.New("unknown upstream")
	}
	if !up.Enabled {
		return "", false, errors.New("switch the upstream on first")
	}
	if args == nil {
		args = map[string]any{}
	}
	if up.IsOpenAPI() {
		return s.CallOpenAPITool(ctx, alias, name, args)
	}
	if name == "" {
		return "", false, errors.New("that tool is not on the list")
	}
	res, rerr, err := s.up.call(s, ctx, up, "tools/call", map[string]any{"name": name, "arguments": args}, aggCallTimeout)
	if err != nil {
		return "", false, err
	}
	if rerr != nil {
		return "", false, errors.New(rerr.Message)
	}
	return toolAnswer(res)
}

// findUpstreamTool asks tools/list (following a few pages) and returns the named tool as a client sees it.
func (s *Server) findUpstreamTool(ctx context.Context, up Upstream, name string) (map[string]any, error) {
	var cursor any
	for page := 0; page < aggMaxPages; page++ {
		params := map[string]any{}
		if cursor != nil {
			params["cursor"] = cursor
		}
		res, rerr, err := s.up.call(s, ctx, up, "tools/list", params, aggTimeout)
		if err != nil {
			return nil, err
		}
		if rerr != nil {
			return nil, errors.New(rerr.Message)
		}
		var lr struct {
			Tools []struct {
				Name        string          `json:"name"`
				Description string          `json:"description"`
				InputSchema json.RawMessage `json:"inputSchema"`
			} `json:"tools"`
			NextCursor string `json:"nextCursor"`
		}
		if json.Unmarshal(res, &lr) != nil {
			return nil, errors.New("tools/list: the result is not a valid tools list")
		}
		for _, t := range lr.Tools {
			if t.Name != name {
				continue
			}
			var schema any = map[string]any{"type": "object"}
			if len(t.InputSchema) > 0 && string(t.InputSchema) != "null" {
				if json.Unmarshal(t.InputSchema, &schema) != nil {
					schema = map[string]any{"type": "object"}
				}
			}
			return map[string]any{"name": t.Name, "description": t.Description, "schema": schema}, nil
		}
		if lr.NextCursor == "" {
			return nil, nil
		}
		cursor = lr.NextCursor
	}
	return nil, nil
}

// toolAnswer turns a tools/call result into text for the admin tester and whether the tool reported an error.
func toolAnswer(res json.RawMessage) (text string, isErr bool, err error) {
	var m map[string]any
	if json.Unmarshal(res, &m) != nil {
		return string(res), false, nil
	}
	isErr, _ = m["isError"].(bool)
	if c, _ := m["content"].([]any); len(c) == 1 {
		if first, _ := c[0].(map[string]any); first != nil {
			if t, ok := first["text"].(string); ok {
				return t, isErr, nil
			}
		}
	}
	pretty, perr := json.MarshalIndent(m, "", "  ")
	if perr != nil {
		return string(res), isErr, nil
	}
	return string(pretty), isErr, nil
}
