// Package videogen provides the generate_video tool: it turns a text
// prompt (optionally with a first-frame image) into a video by routing
// a video-output inference request through the deployment router. No
// model is pinned here — the router's capability-aware selection picks
// a video-capable target (MiniMax Hailuo, ByteDance Seedance) from the
// user's generate policy. Providers return the finished video as a
// download URL, so the tool downloads it immediately (MiniMax URLs
// expire after an hour) and persists it under generated/ in the
// workspace.
package videogen

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/inference"
	"github.com/GizClaw/flowcraft/core/inference/route"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/message/media"
	"github.com/GizClaw/flowcraft/core/telemetry"
	"github.com/GizClaw/flowcraft/core/tool"
	"github.com/GizClaw/flowcraft/core/workspace"
	"github.com/rs/xid"

	"github.com/GizClaw/opencraft/internal/foundation/utils/inferenceext"
)

// Name is the canonical generate_video tool name.
const Name = "generate_video"

// OutDir is the workspace-relative directory generated videos land in.
const OutDir = "generated"

// defaultTimeout bounds the whole operation: provider task polling is
// folded into the unary call and can take minutes, and the download
// must happen before provider URLs expire.
const defaultTimeout = 20 * time.Minute

// maxDownloadBytes caps the downloaded video size. Artifacts beyond it
// are refused before they can exhaust memory.
const maxDownloadBytes = 256 << 20 // 256 MiB

// maxFirstFrameBytes caps one first-frame image read into memory.
const maxFirstFrameBytes = 10 << 20 // 10 MiB

// maxDownloadRedirects bounds provider-issued download redirects.
const maxDownloadRedirects = 10

// videoExtensionID is the provider-carried extension that models
// per-request video knobs (the video_options extension both the MiniMax
// and the ByteDance driver register). Which field a driver accepts is
// decided by its own strict decoder: the tool probes the configured
// providers and attaches only what each one models.
const videoExtensionID = "video_options"

// generateFunc is the generation entry; production wires the router,
// tests inject a fake.
type generateFunc func(
	ctx context.Context,
	req inference.GenerateRequest,
) (inference.GenerateResponse, route.Trace, error)

// Settings carries the provider-specific knobs the settings page
// configured for this tool, keyed by deployment id. They are deployment
// preferences, not per-call arguments: the tool attaches each provider's
// set to that provider only.
type Settings struct {
	ProviderOptions map[string]map[string]any `json:"provider_options,omitempty"`
}

// Tool generates videos through the deployment router. It is safe for
// concurrent use.
type Tool struct {
	ws       workspace.Workspace
	generate generateFunc
	client   *http.Client
	// providerOptions is the configured knob set per deployment id.
	providerOptions map[string]map[string]any
	// extensions renders the provider-addressed video knob fields into
	// typed extensions. It is nil when no router is wired, in which case
	// the knobs that need a provider extension are rejected.
	extensions func(
		call map[string]any,
		configured map[string]map[string]any,
	) (inference.Extensions, error)
}

// New builds the generate_video tool. router is required; nil leaves
// the tool un-wired so Execute fails with a clear internal error.
func New(
	router *route.Router, ws workspace.Workspace, settings Settings,
) (*Tool, error) {
	if ws == nil {
		return nil, errdefs.Validationf(
			"generate_video: workspace is required")
	}
	t := &Tool{
		ws:              ws,
		providerOptions: settings.ProviderOptions,
		client: &http.Client{
			Timeout: defaultTimeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= maxDownloadRedirects {
					return fmt.Errorf(
						"%s: download: stopped after %d redirects",
						Name, maxDownloadRedirects)
				}
				if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
					return fmt.Errorf(
						"%s: download: non-http(s) redirect %q", Name, req.URL)
				}
				return nil
			},
		},
	}
	if router != nil {
		t.generate = func(
			ctx context.Context,
			req inference.GenerateRequest,
		) (inference.GenerateResponse, route.Trace, error) {
			return router.Generate(ctx, req)
		}
		t.extensions = func(
			call map[string]any,
			configured map[string]map[string]any,
		) (inference.Extensions, error) {
			return inferenceext.Build(
				Name, router.Target(), videoExtensionID, call, configured)
		}
	}
	return t, nil
}

// MustNew panics on invalid construction; use in static wiring.
func MustNew(
	router *route.Router, ws workspace.Workspace, settings Settings,
) *Tool {
	t, err := New(router, ws, settings)
	if err != nil {
		panic(err)
	}
	return t
}

var _ tool.Tool = (*Tool)(nil)

// Args is the generate_video tool input.
type Args struct {
	// Prompt is the text description of the video to generate.
	Prompt string `json:"prompt"`
	// Model is an optional router hint ("<provider-id>/<name>" or a
	// bare model name), honored only for a video-capable target.
	Model string `json:"model,omitempty"`
	// FirstFrame is an optional workspace-relative image path used as
	// the first frame (image-to-video).
	FirstFrame string `json:"first_frame,omitempty"`
	// LastFrame is an optional workspace-relative image path used as the
	// closing frame. Alone it asks providers that support it for a
	// closing-frame-only generation.
	LastFrame string `json:"last_frame,omitempty"`
	// ReferenceImages are workspace-relative images for providers that
	// take reference inputs (Seedance / H3 omni-reference). At least
	// three are required: providers read one or two images as the first
	// and last frame.
	ReferenceImages []string `json:"reference_images,omitempty"`
	// ReferenceVideos are absolute http(s) URLs the provider fetches as
	// video references. Local files cannot be referenced this way.
	ReferenceVideos []string `json:"reference_videos,omitempty"`
	// ReferenceAudios are absolute http(s) URLs used as audio
	// references.
	ReferenceAudios []string `json:"reference_audios,omitempty"`
	// DurationMillis is the optional target duration; provider models
	// validate their own tiers (MiniMax: 6s or 10s).
	DurationMillis *int64 `json:"duration_millis,omitempty"`
	// Resolution is an optional tier token (e.g. "720p", "1080p", "4k").
	Resolution string `json:"resolution,omitempty"`
	// AspectRatio is an optional "W:H" output ratio; provider models
	// accept their own value sets.
	AspectRatio string `json:"aspect_ratio,omitempty"`
	// Seed fixes the provider's sampling seed where the model supports
	// one.
	Seed *int64 `json:"seed,omitempty"`
	// Watermark requests an AIGC watermark when the provider supports it.
	Watermark *bool `json:"watermark,omitempty"`
}

// Definition describes the generate_video tool.
func (t *Tool) Definition() message.ToolDefinition {
	return message.DefineSchema(
		Name,
		"Generates a video from a text prompt, optionally guided by "+
			"frame images (first/last frame) or reference inputs. The "+
			"request is routed through the configured inference router, "+
			"which selects a video-capable model (e.g. MiniMax Hailuo or "+
			"ByteDance Seedance) by output capability; if the router has "+
			"no such model, the call fails with guidance on how to "+
			"configure one. Generation is an asynchronous provider task "+
			"folded into this call and can take minutes; the finished "+
			"video is downloaded under generated/ in the workspace and the "+
			"returned JSON lists the workspace-relative path plus the "+
			"model that produced it. A knob the selected provider does "+
			"not support is reported in the failure instead of being "+
			"silently ignored.",
		message.ToolProperty("prompt", "string",
			"Text description of the video to generate (required)."),
		message.ToolProperty("model", "string",
			`Optional model hint, "<provider-id>/<model>" or a bare model `+
				"name. The router honors it only for a target that can "+
				"serve video output; otherwise the default route applies."),
		message.ToolProperty("first_frame", "string",
			"Optional workspace-relative path to a png/jpg/webp image used as the first frame."),
		message.ToolProperty("last_frame", "string",
			"Optional workspace-relative png/jpg/webp image used as the "+
				"closing frame. With first_frame it sets both bookends; "+
				"alone it needs a provider that supports a closing-frame-only "+
				"input."),
		message.ToolArrayProperty("reference_images",
			"Optional workspace-relative images used as references "+
				"(omni-reference models), at least three: one or two images "+
				"are read as the first and last frame, so use "+
				"first_frame/last_frame for bookends. Mutually exclusive "+
				"with the frame inputs.",
			message.Items("string")),
		message.ToolArrayProperty("reference_videos",
			"Optional absolute http(s) URLs the provider fetches as video "+
				"references; local files have no upload channel.",
			message.Items("string")),
		message.ToolArrayProperty("reference_audios",
			"Optional absolute http(s) URLs used as audio references.",
			message.Items("string")),
		message.ToolProperty("duration_millis", "integer",
			"Optional target duration in milliseconds; providers validate their own tiers."),
		message.ToolProperty("resolution", "string",
			`Optional resolution tier, e.g. "720p", "1080p", or "4k".`),
		message.ToolProperty("aspect_ratio", "string",
			`Optional output ratio as "W:H", e.g. "16:9"; each provider `+
				"accepts its own value set."),
		message.ToolProperty("seed", "integer",
			"Optional sampling seed for reproducible output, where the "+
				"model supports one."),
		message.ToolProperty("watermark", "boolean",
			"Optional AIGC watermark request."),
	).Required("prompt").Build()
}

// Metadata reports the tool's execution metadata: it writes files,
// spends provider quota, and the async provider task can run for many
// minutes, so the tool bounds its own deadline.
func (t *Tool) Metadata() tool.ToolMeta {
	return tool.ToolMeta{
		MutatesState: true,
		SelfTimeout:  true,
	}
}

// Execute routes one text-to-video request through the router, then
// downloads the finished artifact and persists it under generated/.
// Execute implements tool.Tool. The tool result is a single text part;
// the tool has no multimodal output.
func (t *Tool) Execute(ctx context.Context, arguments string) (message.Content, error) {
	out, err := t.execute(ctx, arguments)
	if err != nil {
		return message.Content{}, err
	}
	return message.NewTextContent(out), nil
}

// execute renders the tool's text result.
func (t *Tool) execute(ctx context.Context, arguments string) (string, error) {
	if t.generate == nil {
		return "", errdefs.Internalf("%s: router is not wired", Name)
	}
	var args Args
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return "", errdefs.Validationf(
			"%s: parse arguments: %v", Name, err)
	}
	req, err := t.request(ctx, args)
	if err != nil {
		return "", err
	}

	genCtx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()
	resp, trace, err := t.generate(genCtx, req)
	if err != nil {
		if route.IsKind(err, route.NoRoute) {
			return "", fmt.Errorf(
				"%s: the router has no model that supports video output; "+
					"add a video-capable model (e.g. "+
					"bytedance/doubao-seedance-2-0 or "+
					"minimax/MiniMax-Hailuo-2.3) to "+
					"resources.router.settings.generate in "+
					"~/.opencraft/config/opencraft.yaml and set the "+
					"provider key on the settings page: %w", Name, err)
		}
		return "", inferenceext.Explain(err)
	}
	if err := inferenceext.Verify(Name, resp, req.Extensions); err != nil {
		return "", err
	}

	paths, err := t.saveVideos(ctx, resp)
	if err != nil {
		return "", err
	}
	payload := map[string]any{
		"paths": paths,
		"count": len(paths),
		"model": inferenceext.Label(trace.Executed.ID),
		"hint":  "Videos are workspace-relative; open or reference them by path.",
	}
	if dropped := inferenceext.Dropped(resp); len(dropped) > 0 {
		payload["dropped"] = dropped
	}
	out, err := json.Marshal(payload)
	if err != nil {
		return "", errdefs.Internalf(
			"%s: encode result: %v", Name, err)
	}
	return string(out), nil
}

// request lowers validated arguments into the routed generate request:
// the prompt plus any frame or reference inputs, the canonical video
// intent, the provider-addressed knobs, and the optional model hint.
func (t *Tool) request(
	ctx context.Context, args Args,
) (inference.GenerateRequest, error) {
	prompt := strings.TrimSpace(args.Prompt)
	if prompt == "" {
		return inference.GenerateRequest{}, errdefs.Validationf(
			"%s: prompt is required", Name)
	}
	intent, err := videoIntent(args)
	if err != nil {
		return inference.GenerateRequest{}, err
	}
	parts, call, err := t.inputs(ctx, args)
	if err != nil {
		return inference.GenerateRequest{}, err
	}
	// Providers join the text parts into one prompt and read the media
	// parts in order, so the text travels last and the inputs keep the
	// role order the drivers expect.
	parts = append(parts, message.TextPart{Text: prompt})
	extensions, err := t.providerExtensions(call)
	if err != nil {
		return inference.GenerateRequest{}, err
	}
	return inference.GenerateRequest{
		Input: inference.GenerateInput{
			Role: inference.InputRoleUser,
			Content: inference.InputContent{
				Content: message.Content{Parts: parts},
				Intent:  inference.Intent{Video: intent},
			},
		},
		Extensions: extensions,
		ModelHint:  strings.TrimSpace(args.Model),
	}, nil
}

// videoIntent validates the canonical video controls. Provider-specific
// value sets (duration tiers, resolution, ratios) stay with the drivers,
// which reject what their models cannot serve.
func videoIntent(args Args) (*inference.VideoIntent, error) {
	intent := &inference.VideoIntent{
		DurationMillis: args.DurationMillis,
		Resolution:     strings.ToLower(strings.TrimSpace(args.Resolution)),
		Seed:           args.Seed,
		Watermark:      args.Watermark,
	}
	if raw := strings.TrimSpace(args.AspectRatio); raw != "" {
		ratio := media.AspectRatio(raw)
		if err := ratio.Validate(); err != nil {
			return nil, errdefs.Validationf(
				"%s: aspect_ratio: %v", Name, err)
		}
		intent.AspectRatio = ratio
	}
	return intent, nil
}

// inputs loads the frame or reference inputs. The drivers pick input
// roles by count — one image is the first frame, two are the bookends,
// more (or any reference video/audio) are reference inputs — so the
// combination is validated here instead of letting a provider
// reinterpret the parts.
func (t *Tool) inputs(
	ctx context.Context, args Args,
) ([]message.Part, map[string]any, error) {
	var parts []message.Part
	// The only per-call provider knob left is the closing-frame-only
	// marker: every other provider-specific setting is deployment
	// configuration the settings page owns.
	call := map[string]any{}
	references := len(args.ReferenceImages) > 0 ||
		len(args.ReferenceVideos) > 0 || len(args.ReferenceAudios) > 0
	frames := strings.TrimSpace(args.FirstFrame) != "" ||
		strings.TrimSpace(args.LastFrame) != ""
	if references {
		if frames {
			return nil, nil, errdefs.Validationf(
				"%s: frame inputs and reference inputs are mutually exclusive",
				Name)
		}
		if len(args.ReferenceImages) > 0 && len(args.ReferenceImages) < 3 {
			return nil, nil, errdefs.Validationf(
				"%s: reference_images needs at least three images: "+
					"providers read one or two images as the first and "+
					"last frame, so use first_frame/last_frame for bookends",
				Name)
		}
		for _, path := range args.ReferenceImages {
			part, err := t.readImage(ctx, path, "reference_images")
			if err != nil {
				return nil, nil, err
			}
			parts = append(parts, part)
		}
		for _, raw := range args.ReferenceVideos {
			part, err := referenceVideoPart(raw)
			if err != nil {
				return nil, nil, err
			}
			parts = append(parts, part)
		}
		for _, raw := range args.ReferenceAudios {
			part, err := referenceAudioPart(raw)
			if err != nil {
				return nil, nil, err
			}
			parts = append(parts, part)
		}
		return parts, call, nil
	}
	if path := strings.TrimSpace(args.FirstFrame); path != "" {
		part, err := t.readImage(ctx, path, "first_frame")
		if err != nil {
			return nil, nil, err
		}
		parts = append(parts, part)
	}
	if path := strings.TrimSpace(args.LastFrame); path != "" {
		part, err := t.readImage(ctx, path, "last_frame")
		if err != nil {
			return nil, nil, err
		}
		parts = append(parts, part)
		if strings.TrimSpace(args.FirstFrame) == "" {
			// A single image is the opening frame unless the provider is
			// told otherwise, so a closing-frame-only request needs the
			// knob; a provider that does not model it fails the call.
			call["last_frame_only"] = true
		}
	}
	return parts, call, nil
}

// providerExtensions renders the requested provider-addressed knobs. An
// empty knob set needs no extension; a non-empty one fails loudly when
// no configured provider models it.
func (t *Tool) providerExtensions(
	fields map[string]any,
) (inference.Extensions, error) {
	if len(fields) == 0 && len(t.providerOptions) == 0 {
		return nil, nil
	}
	if t.extensions == nil {
		return nil, errdefs.Internalf("%s: router is not wired", Name)
	}
	return t.extensions(fields, t.providerOptions)
}

// referenceVideoPart wraps one http(s) reference video as a URL-sourced
// part: the provider fetches the URL itself, and inlining a video would
// push the whole file through the request.
func referenceVideoPart(raw string) (message.Part, error) {
	raw = strings.TrimSpace(raw)
	if err := checkHTTPURL(raw); err != nil {
		return nil, errdefs.Validationf(
			"%s: reference_videos: %v", Name, err)
	}
	source, err := media.NewVideoURL(raw, "")
	if err != nil {
		return nil, errdefs.Validationf(
			"%s: reference_videos: %v", Name, err)
	}
	return message.VideoPart{Source: source}, nil
}

// referenceAudioPart wraps one http(s) reference audio clip.
func referenceAudioPart(raw string) (message.Part, error) {
	raw = strings.TrimSpace(raw)
	if err := checkHTTPURL(raw); err != nil {
		return nil, errdefs.Validationf(
			"%s: reference_audios: %v", Name, err)
	}
	source, err := media.NewAudioURL(raw, "")
	if err != nil {
		return nil, errdefs.Validationf(
			"%s: reference_audios: %v", Name, err)
	}
	return message.AudioPart{Source: source}, nil
}

// checkHTTPURL keeps provider-fetched references on http(s): anything
// else has no fetch channel on the provider side.
func checkHTTPURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("must be an absolute http(s) URL, got %q", raw)
	}
	return nil
}

// readImage loads one workspace frame or reference image as an inline
// media source.
func (t *Tool) readImage(
	ctx context.Context, path, role string,
) (message.Part, error) {
	mediaType, err := imageMediaType(path)
	if err != nil {
		return nil, errdefs.Validationf(
			"%s: %s: %v", Name, role, err)
	}
	info, err := t.ws.Stat(ctx, path)
	if err != nil {
		return nil, errdefs.Validationf(
			"%s: %s: stat %s: %v", Name, role, path, err)
	}
	if info.Size() > maxFirstFrameBytes {
		return nil, errdefs.Validationf(
			"%s: %s: %s is %d bytes (limit %d)",
			Name, role, path, info.Size(), maxFirstFrameBytes)
	}
	data, err := t.ws.Read(ctx, path)
	if err != nil {
		return nil, errdefs.Validationf(
			"%s: %s: read %s: %v", Name, role, path, err)
	}
	source, err := media.NewImageBytes(data, mediaType)
	if err != nil {
		return nil, errdefs.Internalf(
			"%s: %s: %v", Name, role, err)
	}
	return message.ImagePart{Source: source}, nil
}

// saveVideos downloads every video part of the response and writes it
// under generated/, returning the workspace-relative paths.
func (t *Tool) saveVideos(
	ctx context.Context,
	resp inference.GenerateResponse,
) ([]string, error) {
	var paths []string
	for index, part := range resp.Message.Content.Parts {
		video, ok := part.(message.VideoPart)
		if !ok {
			continue
		}
		if video.Source.Kind() != media.SourceURL {
			return nil, errdefs.Internalf(
				"%s: generated video %d is not URL-sourced", Name, index)
		}
		data, err := t.download(ctx, video.Source.URL())
		if err != nil {
			return nil, err
		}
		path := OutDir + "/video-" + xid.New().String() +
			videoExtension(video.Source.BaseMediaType())
		if err := t.ws.Write(ctx, path, data); err != nil {
			return nil, err
		}
		paths = append(paths, path)
	}
	if len(paths) == 0 {
		return nil, errdefs.Internalf(
			"%s: router response contained no video parts", Name)
	}
	return paths, nil
}

// download fetches the provider-issued video URL into a temp file,
// then reads it back for the workspace write. The artifact is bounded
// by maxDownloadBytes (the workspace API takes whole-file bytes), an
// oversized declared Content-Length is rejected up front, and the
// stream itself is capped so a slow or malicious provider cannot grow
// the in-memory copy beyond the limit.
func (t *Tool) download(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, errdefs.Internalf("%s: download: %v", Name, err)
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return nil, errdefs.Internalf("%s: download: %v", Name, err)
	}
	defer func() {
		telemetry.WarnErr(ctx, "videogen: close provider response failed",
			resp.Body.Close())
	}()
	if resp.StatusCode != http.StatusOK {
		return nil, errdefs.Internalf(
			"%s: download: provider returned %s", Name, resp.Status)
	}
	if resp.ContentLength > maxDownloadBytes {
		return nil, errdefs.Internalf(
			"%s: download: artifact is %d bytes, exceeds the %d byte cap",
			Name, resp.ContentLength, maxDownloadBytes)
	}
	tmp, err := os.CreateTemp("", "opencraft-video-*")
	if err != nil {
		return nil, errdefs.Internalf("%s: download: %v", Name, err)
	}
	tmpName := tmp.Name()
	defer func() {
		telemetry.WarnErr(ctx, "videogen: remove download temp failed",
			os.Remove(tmpName))
	}()
	n, err := io.Copy(tmp, io.LimitReader(resp.Body, maxDownloadBytes+1))
	if err != nil {
		telemetry.WarnErr(ctx, "videogen: close download temp after copy failure",
			tmp.Close())
		return nil, errdefs.Internalf("%s: download: %v", Name, err)
	}
	if n > maxDownloadBytes {
		telemetry.WarnErr(ctx, "videogen: close oversized download temp",
			tmp.Close())
		return nil, errdefs.Internalf(
			"%s: download: artifact exceeds the %d byte cap",
			Name, maxDownloadBytes)
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		telemetry.WarnErr(ctx, "videogen: close download temp after seek failure",
			tmp.Close())
		return nil, errdefs.Internalf("%s: download: %v", Name, err)
	}
	data, err := io.ReadAll(tmp)
	closeErr := tmp.Close()
	if err != nil {
		return nil, errdefs.Internalf("%s: download: %v", Name, err)
	}
	if closeErr != nil {
		return nil, errdefs.Internalf("%s: download: %v", Name, closeErr)
	}
	return data, nil
}

// imageMediaType maps a first-frame file extension to a media type.
func imageMediaType(path string) (string, error) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		return "image/png", nil
	case ".jpg", ".jpeg":
		return "image/jpeg", nil
	case ".webp":
		return "image/webp", nil
	default:
		return "", fmt.Errorf(
			"image must be png, jpg, or webp, got %q",
			filepath.Ext(path))
	}
}

// videoExtension maps a media type to a file extension, defaulting to
// .mp4 for anything unrecognized. A provider that returns a MOV
// container must land as .mov, or a player would read the bytes back
// with the wrong container.
func videoExtension(mediaType string) string {
	switch mediaType {
	case "video/webm":
		return ".webm"
	case "video/quicktime":
		return ".mov"
	default:
		return ".mp4"
	}
}
