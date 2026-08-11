package httpclient

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestNewClientRetriesTransientStatus(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 3 {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	client := NewClient(WithRetryAttempts(3))
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "ok" {
		t.Fatalf("body = %q", body)
	}
	if got := RetryCountOf(resp); got != 3 {
		t.Errorf("retry count = %d, want 3", got)
	}
	if calls.Load() != 3 {
		t.Errorf("calls = %d, want 3", calls.Load())
	}
}

func TestWithoutRetrySingleAttempt(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := NewClient(WithoutRetry())
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if calls.Load() != 1 {
		t.Errorf("calls = %d, want 1", calls.Load())
	}
	if got := RetryCountOf(resp); got != 0 {
		t.Errorf("retry count = %d, want 0 (retry wrapper disabled)", got)
	}
}

func TestProtocolsBuildWithoutPanic(t *testing.T) {
	for _, opt := range []Option{WithHTTP1(), WithHTTP2(), WithHTTP3()} {
		rt := NewRoundTripper(opt, WithTimeout(0), WithoutRetry())
		if rt == nil {
			t.Fatal("nil round tripper")
		}
	}
}

func TestWithDisableKeepAlives(t *testing.T) {
	for _, opt := range []Option{WithHTTP1(), WithHTTP2()} {
		rt := NewRoundTripper(opt, WithDisableKeepAlives(), WithoutRetry())
		transport, ok := rt.(*http.Transport)
		if !ok {
			t.Fatalf("round tripper type = %T, want *http.Transport", rt)
		}
		if !transport.DisableKeepAlives {
			t.Errorf("DisableKeepAlives = false, want true")
		}
	}

	rt := NewRoundTripper(WithHTTP1(), WithoutRetry())
	transport := rt.(*http.Transport)
	if transport.DisableKeepAlives {
		t.Error("DisableKeepAlives = true by default, want false")
	}
}

func TestNewClientTimeoutOption(t *testing.T) {
	client := NewClient(WithTimeout(123 * 1e9))
	if client.Timeout != 123*1e9 {
		t.Fatalf("timeout = %v", client.Timeout)
	}
}

func TestRetryCountOfUntouched(t *testing.T) {
	resp := &http.Response{Header: http.Header{}}
	if got := RetryCountOf(resp); got != 0 {
		t.Errorf("retry count = %d, want 0", got)
	}
	resp.Header.Set(retryCountHeader, strings.TrimSpace("2"))
	if got := RetryCountOf(resp); got != 2 {
		t.Errorf("retry count = %d, want 2", got)
	}
}
