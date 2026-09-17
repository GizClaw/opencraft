package imagegen

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/inference"
	"github.com/GizClaw/flowcraft/core/inference/model"
	"github.com/GizClaw/flowcraft/core/inference/route"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/message/media"
	"github.com/GizClaw/flowcraft/core/workspace"
)

// pngBytes is a minimal valid-ish PNG container so the fake response
// carries a truthful media type.
var pngBytes = []byte("\x89PNG\r\n\x1a\nfake-image-data")

func fakeImagePart(data []byte) message.Part {
	source, err := media.NewImageBytes(data, "image/png")
	if err != nil {
		panic(err)
	}
	return message.ImagePart{Source: source}
}

func newWorkspace(t *testing.T) workspace.Workspace {
	t.Helper()
	ws, err := workspace.NewLocalWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return ws
}

// gifBytes encodes a tiny GIF, a container the image APIs do not take
// inline, so the reference-image path has to re-encode it.
func gifBytes(t *testing.T) []byte {
	t.Helper()
	img := image.NewPaletted(image.Rect(0, 0, 2, 2), color.Palette{
		color.Black, color.White,
	})
	var buf bytes.Buffer
	if err := gif.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// fakeStream replays queued events and then reports EOF, which is the
// contract decodedGenerateStream keeps.
type fakeStream struct {
	events []inference.GenerateStreamEvent
	result inference.GenerateResponse
	err    error
	index  int
	closed bool
}

func (s *fakeStream) Next(context.Context) (inference.GenerateStreamEvent, error) {
	if s.index < len(s.events) {
		event := s.events[s.index]
		s.index++
		return event, nil
	}
	if s.err != nil {
		return inference.GenerateStreamEvent{}, s.err
	}
	return inference.GenerateStreamEvent{}, io.EOF
}

func (s *fakeStream) Result() (inference.GenerateResponse, error) {
	return s.result, nil
}

func (s *fakeStream) Close() error {
	s.closed = true
	return nil
}

func traceFor(provider, name string) route.Trace {
	return route.Trace{Executed: model.ModelRef{
		ID: model.ModelID{Provider: provider, Name: name},
	}}
}

// resultPayload is the JSON the tool returns to the model.
type resultPayload struct {
	Paths    []string `json:"paths"`
	Count    int      `json:"count"`
	Model    string   `json:"model"`
	Previews []string `json:"previews"`
}

func parseResult(t *testing.T, content message.Content) resultPayload {
	t.Helper()
	var payload resultPayload
	if err := json.Unmarshal([]byte(content.Text()), &payload); err != nil {
		t.Fatalf("result is not JSON: %v\n%s", err, content.Text())
	}
	return payload
}

func TestExecuteSavesGeneratedImage(t *testing.T) {
	ws := newWorkspace(t)
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
						Parts: []message.Part{fakeImagePart(pngBytes)},
					},
				},
			}, traceFor("openai", "gpt-image-2"), nil
		},
	}

	out, err := tool.Execute(context.Background(),
		`{"prompt":"a red fox in snow","size":"1024x1024","output_format":"png"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	intent := gotRequest.Input.Content.Intent.Image
	if intent == nil {
		t.Fatal("request intent has no image output")
	}
	if intent.Size == nil ||
		intent.Size.Width != 1024 || intent.Size.Height != 1024 {
		t.Errorf("intent size = %+v, want 1024x1024", intent.Size)
	}
	if intent.OutputFormat != media.ImageFormatPNG {
		t.Errorf("intent format = %q, want png", intent.OutputFormat)
	}
	// The tool persists the bytes it gets, so it always asks for inline
	// delivery: a provider that defaults to URLs would otherwise hand
	// back a source the tool cannot save.
	if intent.Delivery != media.SourceInline {
		t.Errorf("intent delivery = %q, want inline", intent.Delivery)
	}

	result := parseResult(t, out)
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

// TestExecuteLowersRequestKnobs pins every canonical knob the tool
// accepts onto the routed request: model hint, ratio, count, seed,
// quality, and format.
func TestExecuteLowersRequestKnobs(t *testing.T) {
	var gotRequest inference.GenerateRequest
	tool := &Tool{
		ws: newWorkspace(t),
		generate: func(
			_ context.Context, req inference.GenerateRequest,
		) (inference.GenerateResponse, route.Trace, error) {
			gotRequest = req
			return inference.GenerateResponse{
				Message: message.Message{
					Role: message.RoleAssistant,
					Content: message.Content{
						Parts: []message.Part{fakeImagePart(pngBytes)},
					},
				},
			}, traceFor("openai", "gpt-image-2"), nil
		},
	}
	_, err := tool.Execute(context.Background(), `{
		"prompt": "a poster",
		"model": "openai-inst-a/gpt-image-2",
		"aspect_ratio": "16:9",
		"count": 2,
		"seed": 42,
		"quality": "xhigh",
		"output_format": "webp"
	}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if gotRequest.ModelHint != "openai-inst-a/gpt-image-2" {
		t.Errorf("model hint = %q", gotRequest.ModelHint)
	}
	intent := gotRequest.Input.Content.Intent.Image
	if intent.AspectRatio != media.AspectRatio("16:9") {
		t.Errorf("aspect ratio = %q, want 16:9", intent.AspectRatio)
	}
	if intent.Size != nil {
		t.Errorf("size = %+v, want nil for a ratio request", intent.Size)
	}
	if intent.Count == nil || *intent.Count != 2 {
		t.Errorf("count = %v, want 2", intent.Count)
	}
	if intent.Seed == nil || *intent.Seed != 42 {
		t.Errorf("seed = %v, want 42", intent.Seed)
	}
	if intent.Quality != media.ImageQualityXHigh {
		t.Errorf("quality = %q, want xhigh", intent.Quality)
	}
	if intent.OutputFormat != media.ImageFormatWebP {
		t.Errorf("output format = %q, want webp", intent.OutputFormat)
	}
}

// TestExecuteReferenceImagesAndMask pins the image-to-image path: the
// prompt stays the first input part, every reference image is attached
// inline, and the mask reaches the provider extension as a PNG source.
func TestExecuteReferenceImagesAndMask(t *testing.T) {
	ws := newWorkspace(t)
	ctx := context.Background()
	if err := ws.Write(ctx, "photo.png", pngBytes); err != nil {
		t.Fatal(err)
	}
	if err := ws.Write(ctx, "mask.png", []byte("\x89PNG\r\n\x1a\nmask")); err != nil {
		t.Fatal(err)
	}
	var (
		gotRequest inference.GenerateRequest
		gotFields  map[string]any
	)
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
						Parts: []message.Part{fakeImagePart(pngBytes)},
					},
				},
			}, traceFor("azure", "gpt-image-2"), nil
		},
		extensions: func(fields map[string]any) (inference.Extensions, error) {
			gotFields = fields
			return nil, nil
		},
	}
	_, err := tool.Execute(ctx, `{
		"prompt": "paint the sky",
		"reference_images": ["photo.png"],
		"mask": "mask.png"
	}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	parts := gotRequest.Input.Content.Parts
	if len(parts) != 2 {
		t.Fatalf("input parts = %d, want prompt + one reference image", len(parts))
	}
	if _, ok := parts[0].(message.TextPart); !ok {
		t.Fatalf("first part = %T, want the prompt", parts[0])
	}
	imagePart, ok := parts[1].(message.ImagePart)
	if !ok {
		t.Fatalf("second part = %T, want the reference image", parts[1])
	}
	if imagePart.Source.Kind() != media.SourceInline {
		t.Fatal("reference image must be attached inline")
	}
	if !bytes.Equal(imagePart.Source.Bytes(), pngBytes) {
		t.Error("reference image bytes changed; PNG must pass through verbatim")
	}
	mask, ok := gotFields["mask"].(media.ImageSource)
	if !ok {
		t.Fatalf("extension fields = %+v, want a mask source", gotFields)
	}
	if mask.Kind() != media.SourceInline ||
		mask.BaseMediaType() != "image/png" {
		t.Errorf("mask source = %s %s, want inline image/png",
			mask.Kind(), mask.BaseMediaType())
	}
	if string(mask.Bytes()) != "\x89PNG\r\n\x1a\nmask" {
		t.Error("mask bytes changed; the mask must be sent verbatim")
	}
	if _, ok := gotFields["partial_images"]; ok {
		t.Errorf("partial_images must not be set when not requested: %+v", gotFields)
	}
}

// TestExecuteReencodesForeignReferenceFormat pins the fallback: a
// container the image APIs do not accept inline (here a GIF) is
// decoded and re-encoded as JPEG instead of being sent verbatim.
func TestExecuteReencodesForeignReferenceFormat(t *testing.T) {
	ws := newWorkspace(t)
	ctx := context.Background()
	if err := ws.Write(ctx, "anim.gif", gifBytes(t)); err != nil {
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
						Parts: []message.Part{fakeImagePart(pngBytes)},
					},
				},
			}, traceFor("openai", "gpt-image-2"), nil
		},
	}
	_, err := tool.Execute(ctx,
		`{"prompt":"redraw","reference_images":["anim.gif"]}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	parts := gotRequest.Input.Content.Parts
	imagePart, ok := parts[1].(message.ImagePart)
	if !ok {
		t.Fatalf("second part = %T, want the reference image", parts[1])
	}
	if got := imagePart.Source.BaseMediaType(); got != "image/jpeg" {
		t.Fatalf("re-encoded media type = %q, want image/jpeg", got)
	}
	if !bytes.HasPrefix(imagePart.Source.Bytes(), []byte{0xFF, 0xD8, 0xFF}) {
		t.Fatal("re-encoded bytes are not a JPEG")
	}
}

// TestExecuteStreamsPreviews pins the preview path: partial_images
// switches the call to the streaming shape, interim snapshots are
// saved under generated/previews/, and the final image comes from the
// accumulated stream result.
func TestExecuteStreamsPreviews(t *testing.T) {
	ws := newWorkspace(t)
	ctx := context.Background()
	previewA := []byte("\x89PNG\r\n\x1a\npreview-a")
	previewB := []byte("\x89PNG\r\n\x1a\npreview-b")
	final := []byte("\x89PNG\r\n\x1a\nfinal")
	stream := &fakeStream{
		events: []inference.GenerateStreamEvent{
			{Delta: inference.ImagePartDelta{
				Part: message.ImagePart{
					Source: mustImageSource(t, previewA),
				},
				Interim: true,
			}},
			{Delta: inference.ImagePartDelta{
				Part: message.ImagePart{
					Source: mustImageSource(t, previewB),
				},
				Interim: true,
			}},
			{Delta: inference.ImagePartDelta{Part: message.ImagePart{
				Source: mustImageSource(t, final),
			}}},
		},
		result: inference.GenerateResponse{
			Message: message.Message{
				Role: message.RoleAssistant,
				Content: message.Content{
					Parts: []message.Part{fakeImagePart(final)},
				},
			},
		},
	}
	var gotFields map[string]any
	tool := &Tool{
		ws: ws,
		generate: func(
			_ context.Context, _ inference.GenerateRequest,
		) (inference.GenerateResponse, route.Trace, error) {
			t.Fatal("preview requests must use the streaming shape")
			return inference.GenerateResponse{}, route.Trace{}, nil
		},
		stream: func(
			_ context.Context, req inference.GenerateRequest,
		) (inference.GenerateStream, route.Trace, error) {
			if req.Input.Content.Intent.Image == nil {
				t.Error("stream request carries no image intent")
			}
			return stream, traceFor("openai", "gpt-image-2"), nil
		},
		extensions: func(fields map[string]any) (inference.Extensions, error) {
			gotFields = fields
			return nil, nil
		},
	}
	out, err := tool.Execute(ctx,
		`{"prompt":"a red fox","partial_images":2}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if count, ok := gotFields["partial_images"].(int); !ok || count != 2 {
		t.Fatalf("partial_images extension field = %+v, want 2", gotFields)
	}
	if !stream.closed {
		t.Error("stream must be closed after the call")
	}
	result := parseResult(t, out)
	if len(result.Paths) != 1 {
		t.Fatalf("paths = %v, want the final image", result.Paths)
	}
	stored, err := ws.Read(ctx, result.Paths[0])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stored, final) {
		t.Error("final image bytes mismatch")
	}
	if len(result.Previews) != 2 {
		t.Fatalf("previews = %v, want 2", result.Previews)
	}
	for index, want := range [][]byte{previewA, previewB} {
		path := result.Previews[index]
		if !strings.HasPrefix(path, PreviewDir+"/preview-") ||
			!strings.HasSuffix(path, ".png") {
			t.Errorf("preview path = %q, want under generated/previews/", path)
		}
		got, err := ws.Read(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("preview %d bytes mismatch", index)
		}
	}
}

func TestExecuteValidation(t *testing.T) {
	ws := newWorkspace(t)
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
		{"size and ratio", `{"prompt":"x","size":"1024x1024","aspect_ratio":"1:1"}`,
			"mutually exclusive"},
		{"bad ratio", `{"prompt":"x","aspect_ratio":"16x9"}`,
			"aspect ratio must use width:height"},
		{"zero count", `{"prompt":"x","count":0}`, "count must be positive"},
		{"bad quality", `{"prompt":"x","quality":"ultra"}`,
			"quality must be auto, low, medium, high, xhigh, or max"},
		{"bad format", `{"prompt":"x","output_format":"gif"}`,
			"png, jpeg, or webp"},
		{"previews over cap", `{"prompt":"x","partial_images":4}`,
			"partial_images must be between 0 and 3"},
		{"negative previews", `{"prompt":"x","partial_images":-1}`,
			"partial_images must be between 0 and 3"},
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

// TestExecuteReferenceImageValidation pins the input-shape guards that
// need workspace files.
func TestExecuteReferenceImageValidation(t *testing.T) {
	ws := newWorkspace(t)
	ctx := context.Background()
	if err := ws.Write(ctx, "mask.jpg", []byte{0xFF, 0xD8, 0xFF, 0x00}); err != nil {
		t.Fatal(err)
	}
	if err := ws.Write(ctx, "photo.png", pngBytes); err != nil {
		t.Fatal(err)
	}
	if err := ws.Write(ctx, "mask.png", []byte("\x89PNG\r\n\x1a\nmask")); err != nil {
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
	paths := make([]string, maxReferenceImages+1)
	for i := range paths {
		paths[i] = "photo.png"
	}
	tooMany, err := json.Marshal(Args{Prompt: "x", ReferenceImages: paths})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		args string
		want string
	}{
		{"mask without reference",
			`{"prompt":"x","mask":"mask.jpg"}`,
			"mask requires at least one reference image"},
		{"missing reference",
			`{"prompt":"x","reference_images":["missing.png"]}`,
			"read reference image missing.png"},
		{"too many references", string(tooMany), "at most 16 reference images"},
		{"mask without router knobs",
			`{"prompt":"x","reference_images":["photo.png"],"mask":"mask.png"}`,
			"router is not wired"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tool.Execute(ctx, tc.args)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Execute(%s) error = %v, want containing %q",
					tc.args, err, tc.want)
			}
		})
	}

	// A mask that is not a PNG is rejected before any provider sees it.
	knobs := &Tool{
		ws: ws,
		generate: func(
			_ context.Context, _ inference.GenerateRequest,
		) (inference.GenerateResponse, route.Trace, error) {
			t.Fatal("generate must not be called for invalid input")
			return inference.GenerateResponse{}, route.Trace{}, nil
		},
		extensions: func(map[string]any) (inference.Extensions, error) {
			t.Fatal("no extension must be built for an invalid mask")
			return nil, nil
		},
	}
	_, err = knobs.Execute(ctx,
		`{"prompt":"x","reference_images":["photo.png"],"mask":"mask.jpg"}`)
	if err == nil || !strings.Contains(err.Error(), "must be a PNG image") {
		t.Fatalf("error = %v, want a PNG-mask rejection", err)
	}
}

func TestExecuteNoRouteHint(t *testing.T) {
	tool := &Tool{
		ws: newWorkspace(t),
		generate: func(
			_ context.Context, _ inference.GenerateRequest,
		) (inference.GenerateResponse, route.Trace, error) {
			return inference.GenerateResponse{}, route.Trace{},
				route.NewError(route.NoRoute, model.OperationGenerate,
					errors.New("no image-capable pools"))
		},
	}
	_, err := tool.Execute(context.Background(), `{"prompt":"x"}`)
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

// TestExecuteExplainsRejectionCause pins the first guard: an inference
// rejection renders only its kind and field, so the tool must append
// the readable reason from the cause chain or the model cannot tell why
// the driver refused the knob.
func TestExecuteExplainsRejectionCause(t *testing.T) {
	tool := &Tool{
		ws: newWorkspace(t),
		generate: func(
			_ context.Context, _ inference.GenerateRequest,
		) (inference.GenerateResponse, route.Trace, error) {
			return inference.GenerateResponse{}, route.Trace{},
				inference.NewError(
					inference.InvalidExtension,
					model.OperationGenerate,
					inference.FieldID(
						"extension.openai-inst-a.image_options.mask"),
					errdefs.Validation(errors.New(
						`openai-inst-a: mask requires at least one `+
							`inline reference image`)),
				)
		},
	}
	_, err := tool.Execute(context.Background(), `{"prompt":"x"}`)
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{
		"invalid_extension",
		"mask requires at least one inline reference image",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q: %v", want, err)
		}
	}
}

// TestExecuteVerifiesAppliedKnobs pins the second guard: a successful
// response whose executed provider did not record the requested knob as
// native must fail the call instead of returning an image that ignored
// the constraint.
func TestExecuteVerifiesAppliedKnobs(t *testing.T) {
	ctx := context.Background()
	mask := inference.ExtensionField("mask")
	attached := fakeExtension{
		provider: "openai-inst-a",
		id:       imageExtensionID,
		fields:   []inference.ExtensionField{mask},
	}
	newTool := func(
		t *testing.T,
		meta inference.Metadata,
	) (*Tool, string) {
		t.Helper()
		dir := t.TempDir()
		ws, err := workspace.NewLocalWorkspace(dir)
		if err != nil {
			t.Fatal(err)
		}
		if err := ws.Write(ctx, "photo.png", pngBytes); err != nil {
			t.Fatal(err)
		}
		if err := ws.Write(ctx, "mask.png", pngBytes); err != nil {
			t.Fatal(err)
		}
		return &Tool{
			ws: ws,
			generate: func(
				_ context.Context, _ inference.GenerateRequest,
			) (inference.GenerateResponse, route.Trace, error) {
				resp := inference.GenerateResponse{
					Message: message.Message{
						Role: message.RoleAssistant,
						Content: message.Content{
							Parts: []message.Part{fakeImagePart(pngBytes)},
						},
					},
					Metadata: meta,
				}
				return resp, traceFor(meta.Model.Provider, meta.Model.Name), nil
			},
			extensions: func(map[string]any) (inference.Extensions, error) {
				return inference.Extensions{attached}, nil
			},
		}, dir
	}
	const args = `{"prompt":"paint the sky",
		"reference_images":["photo.png"],"mask":"mask.png"}`

	t.Run("applied", func(t *testing.T) {
		tool, dir := newTool(t, inference.Metadata{
			Model:     model.ModelID{Provider: "openai-inst-a", Name: "gpt-image-2"},
			Operation: model.OperationGenerate,
			Decisions: []inference.Decision{{
				Field:       mask.Qualify(attached),
				Disposition: inference.Native,
			}},
		})
		out, err := tool.Execute(ctx, args)
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if result := parseResult(t, out); len(result.Paths) != 1 {
			t.Fatalf("paths = %v, want the generated image", result.Paths)
		}
		if _, err := os.Stat(filepath.Join(dir, OutDir)); err != nil {
			t.Fatalf("generated images missing: %v", err)
		}
	})

	t.Run("fell back to a provider without the knob", func(t *testing.T) {
		tool, dir := newTool(t, inference.Metadata{
			Model:     model.ModelID{Provider: "bytedance-inst-b", Name: "seedream"},
			Operation: model.OperationGenerate,
		})
		_, err := tool.Execute(ctx, args)
		if err == nil || !strings.Contains(err.Error(), "does not support mask") {
			t.Fatalf("error = %v, want an unsupported-knob rejection", err)
		}
		if _, err := os.Stat(filepath.Join(dir, OutDir)); !os.IsNotExist(err) {
			t.Fatalf("generated dir must stay empty on failure, stat err = %v", err)
		}
	})

	t.Run("knob not applied", func(t *testing.T) {
		tool, _ := newTool(t, inference.Metadata{
			Model:     model.ModelID{Provider: "openai-inst-a", Name: "gpt-image-2"},
			Operation: model.OperationGenerate,
			Decisions: []inference.Decision{{
				Field:       mask.Qualify(attached),
				Disposition: inference.Dropped,
				Reason:      "no mask channel",
			}},
		})
		_, err := tool.Execute(ctx, args)
		if err == nil || !strings.Contains(err.Error(), "did not apply mask") {
			t.Fatalf("error = %v, want a not-applied rejection", err)
		}
	})
}

func TestExecuteNoImageParts(t *testing.T) {
	tool := &Tool{
		ws: newWorkspace(t),
		generate: func(
			_ context.Context, _ inference.GenerateRequest,
		) (inference.GenerateResponse, route.Trace, error) {
			return inference.GenerateResponse{
				Message: message.NewTextMessage(
					message.RoleAssistant, "no image here"),
			}, route.Trace{}, nil
		},
	}
	_, err := tool.Execute(context.Background(), `{"prompt":"x"}`)
	if err == nil || !strings.Contains(err.Error(), "no image parts") {
		t.Fatalf("error = %v, want no-image-parts error", err)
	}
}

func TestExecuteUnwiredRouter(t *testing.T) {
	tool := &Tool{ws: newWorkspace(t)}
	_, err := tool.Execute(context.Background(), `{"prompt":"x"}`)
	if err == nil || !strings.Contains(err.Error(), "router is not wired") {
		t.Fatalf("error = %v, want router-is-not-wired error", err)
	}
}

func TestSniffImageMediaType(t *testing.T) {
	for _, tc := range []struct {
		name string
		data []byte
		want string
	}{
		{"png", pngBytes, "image/png"},
		{"jpeg", []byte{0xFF, 0xD8, 0xFF, 0x00}, "image/jpeg"},
		{"webp", []byte("RIFF\x00\x00\x00\x00WEBPVP8 "), "image/webp"},
		{"gif", []byte("GIF89a"), ""},
		{"empty", nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := sniffImageMediaType(tc.data); got != tc.want {
				t.Fatalf("sniffImageMediaType = %q, want %q", got, tc.want)
			}
		})
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

// fakeExtension is a stand-in typed extension for the decoder probes
// and the applied-knob verification.
type fakeExtension struct {
	provider string
	id       string
	fields   []inference.ExtensionField
}

func (e fakeExtension) ProviderID() string  { return e.provider }
func (e fakeExtension) ExtensionID() string { return e.id }
func (e fakeExtension) ActiveFields() []inference.ExtensionField {
	return e.fields
}
func (fakeExtension) Validate() error              { return nil }
func (e fakeExtension) Clone() inference.Extension { return e }

func mustImageSource(t *testing.T, data []byte) media.ImageSource {
	t.Helper()
	source, err := media.NewImageBytes(data, "image/png")
	if err != nil {
		t.Fatal(err)
	}
	return source
}
