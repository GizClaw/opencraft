// Package fakeprovider implements a scriptable OpenAI-compatible chat
// completions server used by the Tier-2 headless E2E tests. It speaks
// the real HTTP wire format (JSON and SSE streaming), so the tests
// exercise the actual driver, retry, and stream-decoding paths instead
// of stubbing the Go runtime.
package fakeprovider

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// ToolCall is one function call the fake model returns.
type ToolCall struct {
	Name      string
	Arguments string
}

// Reply is one scripted assistant turn.
type Reply struct {
	Text      string
	ToolCalls []ToolCall
}

// Server is a scripted chat-completions endpoint.
type Server struct {
	*httptest.Server
	mu      sync.Mutex
	replies []Reply
	calls   int
}

// New starts a fake provider serving the given reply sequence. The
// last reply repeats for any further calls.
func New(t testing.TB, replies ...Reply) *Server {
	t.Helper()
	s := &Server{replies: replies}
	s.Server = httptest.NewServer(http.HandlerFunc(s.handle))
	t.Cleanup(s.Close)
	return s
}

// Calls returns the number of completion requests received.
func (s *Server) Calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/v1/chat/completions" {
		http.NotFound(w, r)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	var req struct {
		Stream bool `json:"stream"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "decode request", http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	s.calls++
	idx := s.calls - 1
	if idx >= len(s.replies) || len(s.replies) == 0 {
		idx = len(s.replies) - 1
	}
	reply := Reply{}
	if idx >= 0 {
		reply = s.replies[idx]
	}
	s.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	if req.Stream {
		s.writeStream(w, reply)
		return
	}
	if err := json.NewEncoder(w).Encode(s.completion(reply)); err != nil {
		http.Error(w, "encode response", http.StatusInternalServerError)
	}
}

func (s *Server) completion(reply Reply) map[string]any {
	msg := map[string]any{"role": "assistant", "content": reply.Text}
	finish := "stop"
	if len(reply.ToolCalls) > 0 {
		msg["content"] = nil
		var calls []map[string]any
		for i, tc := range reply.ToolCalls {
			calls = append(calls, map[string]any{
				"id":   fmt.Sprintf("call_%d", i+1),
				"type": "function",
				"function": map[string]any{
					"name":      tc.Name,
					"arguments": tc.Arguments,
				},
			})
		}
		msg["tool_calls"] = calls
		finish = "tool_calls"
	}
	return map[string]any{
		"id":      "chatcmpl-fake",
		"object":  "chat.completion",
		"created": 1,
		"model":   "fake-model",
		"choices": []map[string]any{{
			"index":         0,
			"message":       msg,
			"finish_reason": finish,
		}},
		"usage": map[string]any{
			"prompt_tokens":     10,
			"completion_tokens": 5,
			"total_tokens":      15,
		},
	}
}

func (s *Server) writeStream(w http.ResponseWriter, reply Reply) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	var writeErr error
	chunk := func(delta map[string]any, finish any) {
		if writeErr != nil {
			return
		}
		payload := map[string]any{
			"id":      "chatcmpl-fake",
			"object":  "chat.completion.chunk",
			"created": 1,
			"model":   "fake-model",
			"choices": []map[string]any{{
				"index":         0,
				"delta":         delta,
				"finish_reason": finish,
			}},
		}
		data, err := json.Marshal(payload)
		if err != nil {
			writeErr = err
			return
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
			writeErr = err
			return
		}
		flusher.Flush()
	}
	if len(reply.ToolCalls) > 0 {
		var calls []map[string]any
		for i, tc := range reply.ToolCalls {
			calls = append(calls, map[string]any{
				"id":   fmt.Sprintf("call_%d", i+1),
				"type": "function",
				"function": map[string]any{
					"name":      tc.Name,
					"arguments": tc.Arguments,
				},
			})
		}
		chunk(map[string]any{"role": "assistant", "tool_calls": calls}, nil)
		chunk(map[string]any{}, "tool_calls")
	} else {
		chunk(map[string]any{"role": "assistant"}, nil)
		if reply.Text != "" {
			chunk(map[string]any{"content": reply.Text}, nil)
		}
		chunk(map[string]any{}, "stop")
	}
	if _, err := io.WriteString(w, "data: [DONE]\n\n"); err != nil {
		return
	}
	flusher.Flush()
}

// URL returns the base URL including the /v1 suffix the SDK expects.
func (s *Server) URL() string {
	return strings.TrimSuffix(s.Server.URL, "/") + "/v1"
}
