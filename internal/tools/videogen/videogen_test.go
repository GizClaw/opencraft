package videogen

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/inference"
	"github.com/GizClaw/flowcraft/core/inference/route"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/message/media"
	"github.com/GizClaw/flowcraft/core/workspace"
)

var videoBytes = []byte("fake-mp4-bytes")

func videoPart(t *testing.T, url string) message.Part {
	t.Helper()
	source, err := media.NewVideoURL(url, "video/mp4")
	if err != nil {
		t.Fatal(err)
	}
	return message.VideoPart{Source: source}
}

func TestExecuteDownloadsAndSavesVideo(t *testing.T) {
	ws, err := workspace.NewLocalWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter, r *http.Request,
	) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(videoBytes)
	}))
	defer srv.Close()

	var gotRequest inference.GenerateRequest
	tool := &Tool{
		ws:     ws,
		client: &http.Client{},
		generate: func(
			_ context.Context, req inference.GenerateRequest,
		) (inference.GenerateResponse, route.Trace, error) {
			gotRequest = req
			return inference.GenerateResponse{
				Message: message.Message{
					Role: message.RoleAssistant,
					Content: message.Content{
						Parts: []message.Part{videoPart(t, srv.URL)},
					},
				},
			}, route.Trace{
				Executed: inference.ModelRef{
					ID: inference.ModelID{
						Provider: "bytedance",
						Name:     "doubao-seedance-2-0",
					},
				},
			}, nil
		},
	}

	duration := int64(6000)
	watermark := true
	out, err := tool.Execute(context.Background(), `{
		"prompt":"a cat walking in snow",
		"duration_millis":6000,
		"resolution":"720p",
		"watermark":true
	}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	intent := gotRequest.Input.Content.Intent.Video
	if intent == nil {
		t.Fatal("request intent has no video output")
	}
	if intent.DurationMillis == nil || *intent.DurationMillis != duration {
		t.Errorf("intent duration = %v, want 6000", intent.DurationMillis)
	}
	if intent.Resolution != "720p" {
		t.Errorf("intent resolution = %q, want 720p", intent.Resolution)
	}
	if intent.Watermark == nil || *intent.Watermark != watermark {
		t.Errorf("intent watermark = %v, want true", intent.Watermark)
	}

	var result struct {
		Paths []string `json:"paths"`
		Count int      `json:"count"`
		Model string   `json:"model"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("result is not JSON: %v\n%s", err, out)
	}
	if result.Count != 1 || len(result.Paths) != 1 {
		t.Fatalf("count/paths = %d/%v, want 1 video", result.Count, result.Paths)
	}
	if result.Model != "bytedance/doubao-seedance-2-0" {
		t.Errorf("model = %q, want bytedance/doubao-seedance-2-0",
			result.Model)
	}
	path := result.Paths[0]
	if !strings.HasPrefix(path, OutDir+"/video-") ||
		!strings.HasSuffix(path, ".mp4") {
		t.Errorf("path = %q, want under generated/ with .mp4", path)
	}
	data, err := ws.Read(context.Background(), path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(data) != string(videoBytes) {
		t.Errorf("stored bytes mismatch: got %d bytes, want %d",
			len(data), len(videoBytes))
	}
}

func TestExecuteFirstFrameReadsWorkspaceImage(t *testing.T) {
	ws, err := workspace.NewLocalWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := ws.Write(context.Background(), "frame.png", pngBytes); err != nil {
		t.Fatal(err)
	}
	var gotRequest inference.GenerateRequest
	tool := &Tool{
		ws:     ws,
		client: &http.Client{},
		generate: func(
			_ context.Context, req inference.GenerateRequest,
		) (inference.GenerateResponse, route.Trace, error) {
			gotRequest = req
			return inference.GenerateResponse{}, route.Trace{}, nil
		},
	}
	_, err = tool.Execute(context.Background(),
		`{"prompt":"x","first_frame":"frame.png"}`)
	if err == nil || !strings.Contains(err.Error(), "no video parts") {
		t.Fatalf("error = %v, want no-video-parts error", err)
	}
	parts := gotRequest.Input.Content.Parts
	if len(parts) != 2 {
		t.Fatalf("parts = %d, want prompt + first frame", len(parts))
	}
	img, ok := parts[0].(message.ImagePart)
	if !ok {
		t.Fatalf("first part is %T, want ImagePart", parts[0])
	}
	if string(img.Source.Bytes()) != string(pngBytes) {
		t.Error("first frame bytes mismatch")
	}
	if _, ok := parts[1].(message.TextPart); !ok {
		t.Errorf("second part is %T, want TextPart", parts[1])
	}
}

func TestExecuteValidation(t *testing.T) {
	ws, err := workspace.NewLocalWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tool := &Tool{
		ws:     ws,
		client: &http.Client{},
		generate: func(
			_ context.Context, _ inference.GenerateRequest,
		) (inference.GenerateResponse, route.Trace, error) {
			t.Fatal("generate must not be called for invalid input")
			return inference.GenerateResponse{}, route.Trace{}, nil
		},
	}
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		args string
		want string
	}{
		{"missing prompt", `{}`, "prompt is required"},
		{"empty prompt", `{"prompt":"  "}`, "prompt is required"},
		{"bad frame ext", `{"prompt":"x","first_frame":"a.gif"}`,
			"png, jpg, or webp"},
		{"missing frame", `{"prompt":"x","first_frame":"nope.png"}`,
			"first_frame"},
		{"junk json", `{`, "parse arguments"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tool.Execute(ctx, tc.args)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Execute(%s) error = %v, want containing %q",
					tc.args, err, tc.want)
			}
		})
	}
}

func TestExecuteNoRouteHint(t *testing.T) {
	ws, err := workspace.NewLocalWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tool := &Tool{
		ws:     ws,
		client: &http.Client{},
		generate: func(
			_ context.Context, _ inference.GenerateRequest,
		) (inference.GenerateResponse, route.Trace, error) {
			return inference.GenerateResponse{}, route.Trace{},
				route.NewError(route.NoRoute, inference.OperationGenerate,
					errors.New("no video-capable pools"))
		},
	}
	_, err = tool.Execute(context.Background(), `{"prompt":"x"}`)
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{
		"router has no model that supports video output",
		"bytedance/doubao-seedance-2-0",
		"minimax/MiniMax-Hailuo-2.3",
		"resources.router.settings.generate",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q: %v", want, err)
		}
	}
}

func TestExecuteNoVideoParts(t *testing.T) {
	ws, err := workspace.NewLocalWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tool := &Tool{
		ws:     ws,
		client: &http.Client{},
		generate: func(
			_ context.Context, _ inference.GenerateRequest,
		) (inference.GenerateResponse, route.Trace, error) {
			return inference.GenerateResponse{
				Message: message.NewTextMessage(
					message.RoleAssistant, "no video here"),
			}, route.Trace{}, nil
		},
	}
	_, err = tool.Execute(context.Background(), `{"prompt":"x"}`)
	if err == nil || !strings.Contains(err.Error(), "no video parts") {
		t.Fatalf("error = %v, want no-video-parts error", err)
	}
}

func TestExecuteDownloadFailure(t *testing.T) {
	ws, err := workspace.NewLocalWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter, r *http.Request,
	) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	tool := &Tool{
		ws:     ws,
		client: &http.Client{},
		generate: func(
			_ context.Context, _ inference.GenerateRequest,
		) (inference.GenerateResponse, route.Trace, error) {
			return inference.GenerateResponse{
				Message: message.Message{
					Role: message.RoleAssistant,
					Content: message.Content{
						Parts: []message.Part{videoPart(t, srv.URL)},
					},
				},
			}, route.Trace{}, nil
		},
	}
	_, err = tool.Execute(context.Background(), `{"prompt":"x"}`)
	if err == nil || !strings.Contains(err.Error(), "provider returned") {
		t.Fatalf("error = %v, want download failure", err)
	}
}

func TestExecuteUnwiredRouter(t *testing.T) {
	ws, err := workspace.NewLocalWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tool := &Tool{ws: ws, client: &http.Client{}}
	_, err = tool.Execute(context.Background(), `{"prompt":"x"}`)
	if err == nil || !strings.Contains(err.Error(), "router is not wired") {
		t.Fatalf("error = %v, want router-is-not-wired error", err)
	}
}

var pngBytes = []byte("\x89PNG\r\n\x1a\nfake-first-frame")
