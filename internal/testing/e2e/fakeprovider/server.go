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
	// RequestID, when set, is echoed as the provider's x-request-id
	// response header so drivers can capture it (errors carry it on the
	// classified chain, streamed replies on the finish delta).
	RequestID string
	// ResponseID overrides the chat completion id echoed by the API
	// ("chatcmpl-fake" otherwise). Streamed replies mirror it on every
	// chunk.
	ResponseID string
	// Status, when non-zero, makes this reply an OpenAI-shaped HTTP
	// error instead of a completion. Error carries the API message.
	Status int
	Error  string
}

// Server is a scripted chat-completions endpoint.
type Server struct {
	*httptest.Server
	mu      sync.Mutex
	replies []Reply
	calls   int
	hold    *Gate
	bodies  [][]byte
}

// Gate pauses the next completion request until Release. It lets
// integration tests arrange deterministic ordering around an in-flight
// provider call (for example closing a runtime while a run is active).
type Gate struct {
	ready       chan struct{}
	release     chan struct{}
	readyOnce   sync.Once
	releaseOnce sync.Once
}

func newGate() *Gate {
	return &Gate{
		ready:   make(chan struct{}),
		release: make(chan struct{}),
	}
}

// Ready is closed once the gated request reaches the fake provider.
func (g *Gate) Ready() <-chan struct{} {
	if g == nil {
		return nil
	}
	return g.ready
}

// Release unblocks the gated request.
func (g *Gate) Release() {
	if g == nil {
		return
	}
	g.releaseOnce.Do(func() { close(g.release) })
}

func (g *Gate) markReady() {
	if g == nil {
		return
	}
	g.readyOnce.Do(func() { close(g.ready) })
}

// HoldNext returns a gate applied to the next completion request.
func (s *Server) HoldNext() *Gate {
	g := newGate()
	s.mu.Lock()
	s.hold = g
	s.mu.Unlock()
	return g
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

// LastMessages decodes and returns the `messages` array of the most
// recent completion request, so integration tests can assert what
// actually reached the provider (system prompt, world sections, and
// the user turn).
func (s *Server) LastMessages() ([]map[string]any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.bodies) == 0 {
		return nil, nil
	}
	var req struct {
		Messages []map[string]any `json:"messages"`
	}
	if err := json.Unmarshal(s.bodies[len(s.bodies)-1], &req); err != nil {
		return nil, err
	}
	return req.Messages, nil
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
	s.bodies = append(s.bodies, append([]byte(nil), body...))
	idx := s.calls - 1
	if idx >= len(s.replies) || len(s.replies) == 0 {
		idx = len(s.replies) - 1
	}
	reply := Reply{}
	if idx >= 0 {
		reply = s.replies[idx]
	}
	hold := s.hold
	s.hold = nil
	if hold != nil {
		hold.markReady()
	}
	s.mu.Unlock()
	if hold != nil {
		<-hold.release
	}

	if reply.RequestID != "" {
		w.Header().Set("x-request-id", reply.RequestID)
	}
	w.Header().Set("Content-Type", "application/json")
	if reply.Status != 0 {
		w.WriteHeader(reply.Status)
		if err := json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"message": reply.Error,
				"type":    "server_error",
				"code":    "server_error",
			},
		}); err != nil {
			return
		}
		return
	}
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
	responseID := reply.ResponseID
	if responseID == "" {
		responseID = "chatcmpl-fake"
	}
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
		"id":      responseID,
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
	responseID := reply.ResponseID
	if responseID == "" {
		responseID = "chatcmpl-fake"
	}
	var writeErr error
	chunk := func(delta map[string]any, finish any) {
		if writeErr != nil {
			return
		}
		payload := map[string]any{
			"id":      responseID,
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
