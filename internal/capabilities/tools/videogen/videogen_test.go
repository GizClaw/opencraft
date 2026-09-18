package videogen

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/inference"
	"github.com/GizClaw/flowcraft/core/inference/model"
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
			// Hoisted into variables: a nested literal inside the
			// return list is indented differently by go1.25 and go1.27
			// gofmt, and CI pins the older toolchain.
			resp := inference.GenerateResponse{
				Message: message.Message{
					Role: message.RoleAssistant,
					Content: message.Content{
						Parts: []message.Part{videoPart(t, srv.URL)},
					},
				},
			}
			trace := route.Trace{
				Executed: model.ModelRef{
					ID: model.ModelID{
						Provider: "bytedance",
						Name:     "doubao-seedance-2-0",
					},
				},
			}
			return resp, trace, nil
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
	if err := json.Unmarshal([]byte(out.Text()), &result); err != nil {
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

func writePNG(t *testing.T, ws workspace.Workspace, path string) {
	t.Helper()
	if err := ws.Write(context.Background(), path, pngBytes); err != nil {
		t.Fatal(err)
	}
}

// TestReadImageTypesFrameByContent pins that a frame's media type comes
// from its bytes: a JPEG saved as .png is sent as image/jpeg instead of
// a mislabeled part, and text saved as .png is not a frame at all.
func TestReadImageTypesFrameByContent(t *testing.T) {
	ws, err := workspace.NewLocalWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// SOI plus an APP0 segment, the bytes a JPEG starts with.
	jpeg := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10}
	jpeg = append(jpeg, []byte("JFIF\x00")...)
	jpeg = append(jpeg, make([]byte, 16)...)
	for name, data := range map[string][]byte{
		"frame.png": jpeg,
		"notes.png": []byte("just a note\n"),
	} {
		if err := ws.Write(context.Background(), name, data); err != nil {
			t.Fatal(err)
		}
	}
	tool := &Tool{ws: ws, client: &http.Client{}}
	ctx := context.Background()

	part, err := tool.readImage(ctx, "frame.png", "first_frame")
	if err != nil {
		t.Fatal(err)
	}
	imagePart, ok := part.(message.ImagePart)
	if !ok {
		t.Fatalf("part is %T, want ImagePart", part)
	}
	if got := imagePart.Source.MediaType(); got != "image/jpeg" {
		t.Errorf("media type = %q, want image/jpeg", got)
	}

	_, err = tool.readImage(ctx, "notes.png", "first_frame")
	if err == nil || !strings.Contains(err.Error(), "png, jpg, or webp") ||
		!strings.Contains(err.Error(), "text/plain") {
		t.Errorf("text frame error = %v, want a format rejection", err)
	}
}

// TestExecuteLowersRequestKnobs pins the canonical controls and the
// model hint onto the routed request.
func TestExecuteLowersRequestKnobs(t *testing.T) {
	ws, err := workspace.NewLocalWorkspace(t.TempDir())
	if err != nil {
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
	_, err = tool.Execute(context.Background(), `{
		"prompt":"a cat",
		"model":"minimax-inst-a/MiniMax-Hailuo-2.3",
		"aspect_ratio":"16:9",
		"seed":7,
		"resolution":"1080P"
	}`)
	if err == nil || !strings.Contains(err.Error(), "no video parts") {
		t.Fatalf("error = %v, want no-video-parts error", err)
	}
	if gotRequest.ModelHint != "minimax-inst-a/MiniMax-Hailuo-2.3" {
		t.Errorf("model hint = %q", gotRequest.ModelHint)
	}
	intent := gotRequest.Input.Content.Intent.Video
	if intent.AspectRatio != media.AspectRatio("16:9") {
		t.Errorf("aspect ratio = %q, want 16:9", intent.AspectRatio)
	}
	if intent.Seed == nil || *intent.Seed != 7 {
		t.Errorf("seed = %v, want 7", intent.Seed)
	}
	if intent.Resolution != "1080p" {
		t.Errorf("resolution = %q, want the lowercased tier", intent.Resolution)
	}
}

// TestExecuteBookendFrames pins the count-based input contract: two
// frames arrive as ordered image parts, and a lone last_frame carries
// the closing-frame-only knob.
func TestExecuteBookendFrames(t *testing.T) {
	ws, err := workspace.NewLocalWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	writePNG(t, ws, "first.png")
	writePNG(t, ws, "last.png")
	var gotRequest inference.GenerateRequest
	var gotFields map[string]any
	tool := &Tool{
		ws:     ws,
		client: &http.Client{},
		generate: func(
			_ context.Context, req inference.GenerateRequest,
		) (inference.GenerateResponse, route.Trace, error) {
			gotRequest = req
			return inference.GenerateResponse{}, route.Trace{}, nil
		},
		extensions: func(call map[string]any, configured map[string]map[string]any) (inference.Extensions, error) {
			gotFields = call
			return nil, nil
		},
	}
	_, err = tool.Execute(context.Background(),
		`{"prompt":"x","first_frame":"first.png","last_frame":"last.png"}`)
	if err == nil || !strings.Contains(err.Error(), "no video parts") {
		t.Fatalf("error = %v, want no-video-parts error", err)
	}
	parts := gotRequest.Input.Content.Parts
	if len(parts) != 3 {
		t.Fatalf("parts = %d, want first + last + prompt", len(parts))
	}
	for index, want := range []string{"first.png", "last.png"} {
		img, ok := parts[index].(message.ImagePart)
		if !ok {
			t.Fatalf("part %d = %T, want ImagePart", index, parts[index])
		}
		if img.Source.Kind() != media.SourceInline {
			t.Errorf("part %d is %s, want inline", index, img.Source.Kind())
		}
		_ = want
	}
	if _, ok := gotFields["last_frame_only"]; ok {
		t.Errorf("two frames must not set last_frame_only: %+v", gotFields)
	}

	// A lone closing frame asks for the provider knob, because one image
	// is otherwise read as the opening frame.
	if _, err := tool.Execute(
		context.Background(), `{"prompt":"x","last_frame":"last.png"}`,
	); err == nil || !strings.Contains(err.Error(), "no video parts") {
		t.Fatalf("error = %v, want no-video-parts error", err)
	}
	if gotFields["last_frame_only"] != true {
		t.Fatalf("lone last_frame fields = %+v, want last_frame_only", gotFields)
	}
	parts = gotRequest.Input.Content.Parts
	if len(parts) != 2 {
		t.Fatalf("parts = %d, want closing frame + prompt", len(parts))
	}
}

// TestExecuteReferenceInputs pins the omni-reference path: reference
// images stay inline, reference videos and audio ride as provider-
// fetched URLs, and the ambiguous one/two-image shapes are rejected.
func TestExecuteReferenceInputs(t *testing.T) {
	ws, err := workspace.NewLocalWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.png", "b.png", "c.png"} {
		writePNG(t, ws, name)
	}
	var gotRequest inference.GenerateRequest
	var gotFields map[string]any
	tool := &Tool{
		ws:     ws,
		client: &http.Client{},
		generate: func(
			_ context.Context, req inference.GenerateRequest,
		) (inference.GenerateResponse, route.Trace, error) {
			gotRequest = req
			return inference.GenerateResponse{}, route.Trace{}, nil
		},
		extensions: func(call map[string]any, configured map[string]map[string]any) (inference.Extensions, error) {
			gotFields = call
			return nil, nil
		},
	}
	_, err = tool.Execute(context.Background(), `{
		"prompt":"x",
		"reference_images":["a.png","b.png","c.png"],
		"reference_videos":["https://cdn.example/clip.mp4"],
		"reference_audios":["https://cdn.example/track.mp3"]
	}`)
	if err == nil || !strings.Contains(err.Error(), "no video parts") {
		t.Fatalf("error = %v, want no-video-parts error", err)
	}
	parts := gotRequest.Input.Content.Parts
	if len(parts) != 6 {
		t.Fatalf("parts = %d, want 3 images + video + audio + prompt", len(parts))
	}
	for index := range 3 {
		if _, ok := parts[index].(message.ImagePart); !ok {
			t.Errorf("part %d = %T, want ImagePart", index, parts[index])
		}
	}
	video, ok := parts[3].(message.VideoPart)
	if !ok || video.Source.Kind() != media.SourceURL {
		t.Fatalf("part 3 = %#v, want a URL video reference", parts[3])
	}
	audio, ok := parts[4].(message.AudioPart)
	if !ok || audio.Source.Kind() != media.SourceURL {
		t.Fatalf("part 4 = %#v, want a URL audio reference", parts[4])
	}
	if _, ok := parts[5].(message.TextPart); !ok {
		t.Fatalf("part 5 = %T, want the prompt last", parts[5])
	}
	if _, ok := gotFields["omni_reference_task_type"]; ok {
		t.Errorf("knobs must stay empty when not requested: %+v", gotFields)
	}

	for _, tc := range []struct {
		name string
		args string
		want string
	}{
		{"two reference images",
			`{"prompt":"x","reference_images":["a.png","b.png"]}`,
			"at least three images"},
		{"frames and references",
			`{"prompt":"x","first_frame":"a.png","reference_images":["a.png","b.png","c.png"]}`,
			"mutually exclusive"},
		{"local reference video",
			`{"prompt":"x","reference_videos":["clip.mp4"]}`,
			"absolute http(s) URL"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tool.Execute(context.Background(), tc.args)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Execute(%s) error = %v, want containing %q",
					tc.args, err, tc.want)
			}
		})
	}
}

// TestExecuteConfiguredProviderOptions pins the new home of the
// driver-specific knobs: the settings page configures them per
// deployment, the tool hands that set to the extension builder (which
// attaches each provider's own), and the model-facing arguments carry
// none of them.
func TestExecuteConfiguredProviderOptions(t *testing.T) {
	ws, err := workspace.NewLocalWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	configured := map[string]map[string]any{
		"bytedance-inst-a": {
			"camera_fixed":             true,
			"generate_audio":           true,
			"service_tier":             "flex",
			"output_format":            "mov",
			"omni_reference_task_type": "extend",
			"web_search":               true,
			"callback_url":             "https://example/hook",
			"safety_identifier":        "user-1",
			"priority":                 5,
			"execution_expires_after":  7200,
		},
	}
	var (
		gotCall       map[string]any
		gotConfigured map[string]map[string]any
	)
	tool := &Tool{
		ws:              ws,
		client:          &http.Client{},
		providerOptions: configured,
		generate: func(
			_ context.Context, _ inference.GenerateRequest,
		) (inference.GenerateResponse, route.Trace, error) {
			return inference.GenerateResponse{}, route.Trace{}, nil
		},
		extensions: func(call map[string]any, configured map[string]map[string]any) (inference.Extensions, error) {
			gotCall = call
			gotConfigured = configured
			return nil, nil
		},
	}
	_, err = tool.Execute(context.Background(), `{"prompt":"x"}`)
	if err == nil || !strings.Contains(err.Error(), "no video parts") {
		t.Fatalf("error = %v, want no-video-parts error", err)
	}
	if len(gotCall) != 0 {
		t.Errorf("call knobs = %+v, want none", gotCall)
	}
	if len(gotConfigured["bytedance-inst-a"]) != len(configured["bytedance-inst-a"]) {
		t.Errorf("configured options = %+v, want the settings set", gotConfigured)
	}

	// A configured set without a wired router cannot be rendered.
	bare := &Tool{ws: ws, client: &http.Client{}, providerOptions: configured,
		generate: func(
			_ context.Context, _ inference.GenerateRequest,
		) (inference.GenerateResponse, route.Trace, error) {
			return inference.GenerateResponse{}, route.Trace{}, nil
		},
	}
	_, err = bare.Execute(context.Background(), `{"prompt":"x"}`)
	if err == nil || !strings.Contains(err.Error(), "router is not wired") {
		t.Fatalf("error = %v, want router-is-not-wired", err)
	}
}

// TestExecuteConfiguredOptionsSurviveWithoutCallKnobs pins that a
// configured set alone still reaches the extension builder.
func TestExecuteConfiguredOptionsSurviveWithoutCallKnobs(t *testing.T) {
	ws, err := workspace.NewLocalWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	called := false
	tool := &Tool{
		ws:              ws,
		client:          &http.Client{},
		providerOptions: map[string]map[string]any{"minimax-inst-a": {"prompt_optimizer": false}},
		generate: func(
			_ context.Context, _ inference.GenerateRequest,
		) (inference.GenerateResponse, route.Trace, error) {
			return inference.GenerateResponse{}, route.Trace{}, nil
		},
		extensions: func(_ map[string]any, configured map[string]map[string]any) (inference.Extensions, error) {
			called = true
			if _, ok := configured["minimax-inst-a"]; !ok {
				t.Errorf("configured = %+v", configured)
			}
			return nil, nil
		},
	}
	if _, err := tool.Execute(context.Background(), `{"prompt":"x"}`); err == nil {
		t.Fatal("expected the no-video-parts error")
	}
	if !called {
		t.Error("configured options must reach the extension builder")
	}
}

// TestExecuteVerifiesAppliedKnobs pins the post-call guard: a provider
// that never received the knob (route fallback) must fail the call
// instead of returning a video without it.
func TestExecuteVerifiesAppliedKnobs(t *testing.T) {
	ws, err := workspace.NewLocalWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	attached := fakeExtension{
		provider: "bytedance-inst-a",
		id:       videoExtensionID,
		fields:   []inference.ExtensionField{"camera_fixed"},
	}
	tool := &Tool{
		ws:              ws,
		client:          &http.Client{},
		providerOptions: map[string]map[string]any{"bytedance-inst-a": {"camera_fixed": true}},
		generate: func(
			_ context.Context, _ inference.GenerateRequest,
		) (inference.GenerateResponse, route.Trace, error) {
			return inference.GenerateResponse{
				Metadata: inference.Metadata{
					Model: model.ModelID{
						Provider: "minimax-inst-b",
						Name:     "MiniMax-Hailuo-2.3",
					},
				},
			}, route.Trace{}, nil
		},
		extensions: func(call map[string]any, configured map[string]map[string]any) (inference.Extensions, error) {
			return inference.Extensions{attached}, nil
		},
	}
	_, err = tool.Execute(context.Background(), `{"prompt":"x"}`)
	if err == nil || !strings.Contains(err.Error(), "does not support camera_fixed") {
		t.Fatalf("error = %v, want an unsupported-knob rejection", err)
	}
}

// TestExecuteExplainsRejectionCause pins the cause-chain surfacing: an
// inference rejection renders only kind and field, so the tool has to
// append the readable reason.
func TestExecuteExplainsRejectionCause(t *testing.T) {
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
				inference.NewError(
					inference.UnsupportedFeature,
					model.OperationGenerate,
					inference.FieldGenerateIntentVideoResolution,
					errdefs.Validation(errors.New(
						"MiniMax-Hailuo-2.3 serves 768P/1080P tiers, not \"4k\"")),
				)
		},
	}
	_, err = tool.Execute(context.Background(),
		`{"prompt":"x","resolution":"4k"}`)
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{
		"unsupported_feature",
		`serves 768P/1080P tiers, not "4k"`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q: %v", want, err)
		}
	}
}

func TestVideoExtension(t *testing.T) {
	for mediaType, want := range map[string]string{
		"video/mp4":              ".mp4",
		"video/webm":             ".webm",
		"video/quicktime":        ".mov",
		"application/x-matroska": ".mp4",
		"":                       ".mp4",
	} {
		if got := videoExtension(mediaType); got != want {
			t.Errorf("videoExtension(%q) = %q, want %q", mediaType, got, want)
		}
	}
}

func TestExecuteValidation(t *testing.T) {
	ws, err := workspace.NewLocalWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// A real GIF and a text file under image names: the frame format is
	// decided by the bytes, so both are rejected for what they are.
	for name, data := range map[string][]byte{
		"a.gif":    append([]byte("GIF89a"), make([]byte, 16)...),
		"text.png": []byte("just a note\n"),
	} {
		if err := ws.Write(context.Background(), name, data); err != nil {
			t.Fatal(err)
		}
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
		{"bad frame format", `{"prompt":"x","first_frame":"a.gif"}`,
			"png, jpg, or webp"},
		{"text under an image name", `{"prompt":"x","first_frame":"text.png"}`,
			"png, jpg, or webp"},
		{"missing frame", `{"prompt":"x","first_frame":"nope.png"}`,
			"first_frame"},
		{"bad ratio", `{"prompt":"x","aspect_ratio":"16x9"}`,
			"aspect ratio must use width:height"},
		// The driver knobs left the schema for the Tools settings tab, so
		// a stale call must fail loudly instead of being dropped.
		{"removed provider knob", `{"prompt":"x","camera_fixed":true}`,
			`unknown field "camera_fixed"`},
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
				route.NewError(route.NoRoute, model.OperationGenerate,
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

func TestExecuteRejectsOversizedContentLength(t *testing.T) {
	ws, err := workspace.NewLocalWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter, r *http.Request,
	) {
		w.Header().Set("Content-Length",
			strconv.FormatInt(maxDownloadBytes+1, 10))
		_, _ = w.Write([]byte("x"))
	}))
	defer srv.Close()

	tool := &Tool{
		ws:     ws,
		client: &http.Client{Timeout: 5 * time.Second},
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
	if err == nil || !strings.Contains(err.Error(), "exceeds the") {
		t.Fatalf("Execute error = %v, want size-cap rejection", err)
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

// fakeExtension stands in for a typed provider extension in the
// applied-knob verification.
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
