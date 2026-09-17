package websearch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/GizClaw/flowcraft/core/errdefs"
)

// mcpClient issues one stateless JSON-RPC tools/call against a hosted
// MCP server over streamable HTTP. Exa and Parallel both accept a
// single POST and answer either with a JSON body or with a one-event
// SSE stream; parseMCPResponse handles both.
type mcpClient struct {
	http *httpClient
}

// mcpRequest is the JSON-RPC envelope both vendors accept.
type mcpRequest struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      int            `json:"id"`
	Method  string         `json:"method"`
	Params  mcpToolCallArg `json:"params"`
}

type mcpToolCallArg struct {
	Name      string `json:"name"`
	Arguments any    `json:"arguments"`
}

// call performs one tools/call and returns the first text content
// block. headers carry provider auth; endpoint is used verbatim (the
// callers append their key query parameter when the vendor needs it).
func (c *mcpClient) call(
	ctx context.Context,
	endpoint string,
	headers map[string]string,
	tool string,
	args any,
) (string, error) {
	payload, err := json.Marshal(mcpRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params:  mcpToolCallArg{Name: tool, Arguments: args},
	})
	if err != nil {
		return "", errdefs.Internalf(
			"web_search: encode %s request: %v", tool, err)
	}
	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", errdefs.Internalf(
			"web_search: build %s request: %v", tool, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range headers {
		if v != "" {
			req.Header.Set(k, v)
		}
	}
	body, err := c.http.do(ctx, tool, req)
	if err != nil {
		return "", err
	}
	text, err := parseMCPResponse(body)
	if err != nil {
		return "", fmt.Errorf("web_search: %s: %w", tool, err)
	}
	return text, nil
}

// mcpError is one JSON-RPC error object.
type mcpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// mcpEnvelope is the subset of a JSON-RPC response opencraft reads.
type mcpEnvelope struct {
	Result *struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	} `json:"result"`
	Error *mcpError `json:"error"`
}

// parseMCPResponse extracts the text payload from either a direct JSON
// response or an SSE stream carrying one. It returns an error when the
// body carries neither a result nor a protocol error, so a changed
// response shape surfaces as a failure instead of "no results".
func parseMCPResponse(body []byte) (string, error) {
	if text, err := parseMCPPayload(body); err != nil {
		return "", err
	} else if text != nil {
		return *text, nil
	}
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimRight(line, "\r")
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" {
			continue
		}
		text, err := parseMCPPayload([]byte(payload))
		if err != nil {
			return "", err
		}
		if text != nil {
			return *text, nil
		}
	}
	return "", errors.New("unexpected MCP response shape")
}

// parseMCPPayload decodes one JSON payload. A nil text with a nil
// error means "not a JSON-RPC payload" (the caller may try SSE lines).
func parseMCPPayload(raw []byte) (*string, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, nil
	}
	var env mcpEnvelope
	if err := json.Unmarshal(trimmed, &env); err != nil {
		return nil, fmt.Errorf("decode MCP response: %w", err)
	}
	if env.Error != nil {
		return nil, errdefs.NotAvailablef(
			"MCP error %d: %s", env.Error.Code, env.Error.Message)
	}
	if env.Result == nil {
		return nil, nil
	}
	var text string
	for _, c := range env.Result.Content {
		if c.Type == "text" && c.Text != "" {
			text = c.Text
			break
		}
	}
	if env.Result.IsError {
		if text == "" {
			text = "tool reported an error"
		}
		return nil, errdefs.NotAvailablef("%s", text)
	}
	if text == "" {
		return nil, errors.New("MCP result carried no text content")
	}
	return &text, nil
}
