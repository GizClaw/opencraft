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
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/inference"
	"github.com/GizClaw/flowcraft/core/inference/route"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/message/media"
	"github.com/GizClaw/flowcraft/core/tool"
	"github.com/GizClaw/flowcraft/core/workspace"
	"github.com/rs/xid"
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

// generateFunc is the generation entry; production wires the router,
// tests inject a fake.
type generateFunc func(
	ctx context.Context,
	req inference.GenerateRequest,
) (inference.GenerateResponse, route.Trace, error)

// Tool generates videos through the deployment router. It is safe for
// concurrent use.
type Tool struct {
	ws       workspace.Workspace
	generate generateFunc
	client   *http.Client
}

// New builds the generate_video tool. router is required; nil leaves
// the tool un-wired so Execute fails with a clear internal error.
func New(router *route.Router, ws workspace.Workspace) (*Tool, error) {
	if ws == nil {
		return nil, errdefs.Validationf(
			"generate_video: workspace is required")
	}
	t := &Tool{
		ws: ws,
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
	}
	return t, nil
}

// MustNew panics on invalid construction; use in static wiring.
func MustNew(router *route.Router, ws workspace.Workspace) *Tool {
	t, err := New(router, ws)
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
	// FirstFrame is an optional workspace-relative image path used as
	// the first frame (image-to-video).
	FirstFrame string `json:"first_frame,omitempty"`
	// DurationMillis is the optional target duration; provider models
	// validate their own tiers (MiniMax: 6s or 10s).
	DurationMillis *int64 `json:"duration_millis,omitempty"`
	// Resolution is an optional tier token (e.g. "720p", "1080p", "4k").
	Resolution string `json:"resolution,omitempty"`
	// Watermark requests an AIGC watermark when the provider supports it.
	Watermark *bool `json:"watermark,omitempty"`
}

// Definition describes the generate_video tool.
func (t *Tool) Definition() message.ToolDefinition {
	return message.DefineSchema(
		Name,
		"Generates a video from a text prompt, optionally using a "+
			"workspace image as the first frame. The request is routed "+
			"through the configured inference router, which selects a "+
			"video-capable model (e.g. MiniMax Hailuo or ByteDance "+
			"Seedance) by output capability; if the router has no such "+
			"model, the call fails with guidance on how to configure one. "+
			"Generation is an asynchronous provider task folded into this "+
			"call and can take minutes. The finished video is downloaded "+
			"under generated/ in the workspace and the returned JSON lists "+
			"the workspace-relative path plus the model that produced it.",
		message.ToolProperty("prompt", "string",
			"Text description of the video to generate (required)."),
		message.ToolProperty("first_frame", "string",
			"Optional workspace-relative path to a png/jpg/webp image used as the first frame."),
		message.ToolProperty("duration_millis", "integer",
			"Optional target duration in milliseconds; providers validate their own tiers."),
		message.ToolProperty("resolution", "string",
			`Optional resolution tier, e.g. "720p", "1080p", or "4k".`),
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
func (t *Tool) Execute(ctx context.Context, arguments string) (string, error) {
	if t.generate == nil {
		return "", errdefs.Internalf("%s: router is not wired", Name)
	}
	var args Args
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return "", errdefs.Validationf(
			"%s: parse arguments: %v", Name, err)
	}
	args.Prompt = strings.TrimSpace(args.Prompt)
	if args.Prompt == "" {
		return "", errdefs.Validationf("%s: prompt is required", Name)
	}

	req := inference.GenerateRequest{
		Input: inference.GenerateInput{
			Role: inference.InputRoleUser,
			Content: inference.InputContent{
				Content: message.Content{Parts: []message.Part{
					message.TextPart{Text: args.Prompt},
				}},
			},
		},
	}
	intent := &inference.VideoIntent{
		DurationMillis: args.DurationMillis,
		Resolution:     strings.ToLower(strings.TrimSpace(args.Resolution)),
		Watermark:      args.Watermark,
	}
	if args.FirstFrame != "" {
		image, err := t.readFirstFrame(ctx, args.FirstFrame)
		if err != nil {
			return "", err
		}
		// Providers treat the first image as the first frame; put it
		// before the prompt text so ordering is unambiguous.
		req.Input.Content.Parts = append(
			[]message.Part{image}, req.Input.Content.Parts...)
	}
	req.Input.Content.Intent.Video = intent

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
		return "", err
	}

	paths, err := t.saveVideos(ctx, resp)
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(map[string]any{
		"paths": paths,
		"count": len(paths),
		"model": modelLabel(trace.Executed.ID),
		"hint":  "Videos are workspace-relative; open or reference them by path.",
	})
	if err != nil {
		return "", errdefs.Internalf(
			"%s: encode result: %v", Name, err)
	}
	return string(payload), nil
}

// readFirstFrame loads a workspace image as an inline media source for
// image-to-video generation.
func (t *Tool) readFirstFrame(
	ctx context.Context, path string,
) (message.ImagePart, error) {
	mediaType, err := imageMediaType(path)
	if err != nil {
		return message.ImagePart{}, errdefs.Validationf(
			"%s: first_frame: %v", Name, err)
	}
	info, err := t.ws.Stat(ctx, path)
	if err != nil {
		return message.ImagePart{}, errdefs.Validationf(
			"%s: first_frame: stat %s: %v", Name, path, err)
	}
	if info.Size() > maxFirstFrameBytes {
		return message.ImagePart{}, errdefs.Validationf(
			"%s: first_frame: %s is %d bytes (limit %d)",
			Name, path, info.Size(), maxFirstFrameBytes)
	}
	data, err := t.ws.Read(ctx, path)
	if err != nil {
		return message.ImagePart{}, errdefs.Validationf(
			"%s: first_frame: read %s: %v", Name, path, err)
	}
	source, err := media.NewImageBytes(data, mediaType)
	if err != nil {
		return message.ImagePart{}, errdefs.Internalf(
			"%s: first_frame: %v", Name, err)
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
	defer func() { _ = resp.Body.Close() }()
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
	defer func() { _ = os.Remove(tmpName) }()
	n, err := io.Copy(tmp, io.LimitReader(resp.Body, maxDownloadBytes+1))
	if err != nil {
		_ = tmp.Close()
		return nil, errdefs.Internalf("%s: download: %v", Name, err)
	}
	if n > maxDownloadBytes {
		_ = tmp.Close()
		return nil, errdefs.Internalf(
			"%s: download: artifact exceeds the %d byte cap",
			Name, maxDownloadBytes)
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		_ = tmp.Close()
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
// .mp4 for anything unrecognized.
func videoExtension(mediaType string) string {
	switch mediaType {
	case "video/webm":
		return ".webm"
	default:
		return ".mp4"
	}
}

// modelLabel renders a model id as "provider/name" (or just "name"
// when the provider is empty).
func modelLabel(id inference.ModelID) string {
	if id.Provider == "" {
		return id.Name
	}
	return id.Provider + "/" + id.Name
}
