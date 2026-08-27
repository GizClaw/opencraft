package imagegen

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/inference"
	"github.com/GizClaw/flowcraft/core/inference/route"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/message/media"
	"github.com/GizClaw/flowcraft/core/workspace"
)

// pngBytes is a minimal valid-ish PNG container so the fake response
// carries a truthful media type.
var pngBytes = []byte("\x89PNG\r\n\x1a\nfake-image-data")

func fakeImagePart() message.Part {
	source, err := media.NewImageBytes(pngBytes, "image/png")
	if err != nil {
		panic(err)
	}
	return message.ImagePart{Source: source}
}

func TestExecuteSavesGeneratedImage(t *testing.T) {
	ws, err := workspace.NewLocalWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var gotRequest inference.GenerateRequest
	tool := &Tool{
		ws: ws,
		generate: func(
			_ context.Context, req inference.GenerateRequest,
		) (inference.GenerateResponse, route.Trace, error) {
			gotRequest = req
			return inference.GenerateResponse{
				Message: message.Message{
					Role: message.RoleAssistant,
					Content: message.Content{
						Parts: []message.Part{fakeImagePart()},
					},
				},
			}, route.Trace{
				Executed: inference.ModelRef{
					ID: inference.ModelID{
						Provider: "openai",
						Name:     "gpt-image-2",
					},
				},
			}, nil
		},
	}

	out, err := tool.Execute(context.Background(),
		`{"prompt":"a red fox in snow","size":"1024x1024","output_format":"png"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if gotRequest.Input.Content.Intent.Image == nil {
		t.Fatal("request intent has no image output")
	}
	if gotRequest.Input.Content.Intent.Image.Size == nil ||
		gotRequest.Input.Content.Intent.Image.Size.Width != 1024 ||
		gotRequest.Input.Content.Intent.Image.Size.Height != 1024 {
		t.Errorf("intent size = %+v, want 1024x1024",
			gotRequest.Input.Content.Intent.Image.Size)
	}
	if gotRequest.Input.Content.Intent.Image.OutputFormat != media.ImageFormatPNG {
		t.Errorf("intent format = %q, want png",
			gotRequest.Input.Content.Intent.Image.OutputFormat)
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
		t.Fatalf("count/paths = %d/%v, want 1 image", result.Count, result.Paths)
	}
	if result.Model != "openai/gpt-image-2" {
		t.Errorf("model = %q, want openai/gpt-image-2", result.Model)
	}
	path := result.Paths[0]
	if !strings.HasPrefix(path, OutDir+"/image-") ||
		!strings.HasSuffix(path, ".png") {
		t.Errorf("path = %q, want under generated/ with .png", path)
	}
	data, err := ws.Read(context.Background(), path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(data) != string(pngBytes) {
		t.Errorf("stored bytes mismatch: got %d bytes, want %d",
			len(data), len(pngBytes))
	}
}

func TestExecuteValidation(t *testing.T) {
	ws, err := workspace.NewLocalWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tool := &Tool{
		ws: ws,
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
		{"bad size", `{"prompt":"x","size":"square"}`, "size must be WxH"},
		{"negative size", `{"prompt":"x","size":"-1x10"}`, "positive integers"},
		{"bad format", `{"prompt":"x","output_format":"gif"}`, "png, jpeg, or webp"},
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
		ws: ws,
		generate: func(
			_ context.Context, _ inference.GenerateRequest,
		) (inference.GenerateResponse, route.Trace, error) {
			return inference.GenerateResponse{}, route.Trace{},
				route.NewError(route.NoRoute, inference.OperationGenerate,
					errors.New("no image-capable pools"))
		},
	}
	_, err = tool.Execute(context.Background(), `{"prompt":"x"}`)
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{
		"router has no model that supports image output",
		"openai/gpt-image-2",
		"resources.router.settings.generate",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q: %v", want, err)
		}
	}
}

func TestExecuteNoImageParts(t *testing.T) {
	ws, err := workspace.NewLocalWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tool := &Tool{
		ws: ws,
		generate: func(
			_ context.Context, _ inference.GenerateRequest,
		) (inference.GenerateResponse, route.Trace, error) {
			return inference.GenerateResponse{
				Message: message.NewTextMessage(
					message.RoleAssistant, "no image here"),
			}, route.Trace{}, nil
		},
	}
	_, err = tool.Execute(context.Background(), `{"prompt":"x"}`)
	if err == nil || !strings.Contains(err.Error(), "no image parts") {
		t.Fatalf("error = %v, want no-image-parts error", err)
	}
}

func TestExecuteUnwiredRouter(t *testing.T) {
	ws, err := workspace.NewLocalWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tool := &Tool{ws: ws}
	_, err = tool.Execute(context.Background(), `{"prompt":"x"}`)
	if err == nil || !strings.Contains(err.Error(), "router is not wired") {
		t.Fatalf("error = %v, want router-is-not-wired error", err)
	}
}

func TestParseSize(t *testing.T) {
	for _, tc := range []struct {
		raw    string
		width  int
		height int
		ok     bool
	}{
		{"1024x1024", 1024, 1024, true},
		{" 1536x1024 ", 1536, 1024, true},
		{"1024X768", 1024, 768, true},
		{"square", 0, 0, false},
		{"1x", 0, 0, false},
		{"0x10", 0, 0, false},
	} {
		w, h, err := parseSize(tc.raw)
		if (err == nil) != tc.ok {
			t.Errorf("parseSize(%q) error = %v, want ok=%v",
				tc.raw, err, tc.ok)
			continue
		}
		if err == nil && (w != tc.width || h != tc.height) {
			t.Errorf("parseSize(%q) = %dx%d, want %dx%d",
				tc.raw, w, h, tc.width, tc.height)
		}
	}
}

func TestExtensionFor(t *testing.T) {
	for mediaType, want := range map[string]string{
		"image/png":  ".png",
		"image/jpeg": ".jpg",
		"image/webp": ".webp",
		"image/avif": ".png",
		"":           ".png",
	} {
		if got := extensionFor(mediaType); got != want {
			t.Errorf("extensionFor(%q) = %q, want %q", mediaType, got, want)
		}
	}
}
