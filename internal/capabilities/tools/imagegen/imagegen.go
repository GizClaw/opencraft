// Package imagegen provides the generate_image tool: it turns a text
// prompt into raster images by routing an image-output inference
// request through the deployment router. No model is pinned here — the
// router's capability-aware selection picks an image-capable target
// (e.g. gpt-image) from the user's generate policy, and the returned
// image parts are written under generated/ in the workspace.
package imagegen

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/inference"
	"github.com/GizClaw/flowcraft/core/inference/route"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/message/media"
	"github.com/GizClaw/flowcraft/core/tool"
	"github.com/GizClaw/flowcraft/core/workspace"
	"github.com/rs/xid"
)

// Name is the canonical generate_image tool name.
const Name = "generate_image"

// OutDir is the workspace-relative directory generated images land in.
const OutDir = "generated"

// generateFunc is the generation entry; production wires the router,
// tests inject a fake.
type generateFunc func(
	ctx context.Context,
	req inference.GenerateRequest,
) (inference.GenerateResponse, route.Trace, error)

// Tool generates images through the deployment router. It is safe for
// concurrent use: the router and workspace are shared, and no mutable
// state is kept per call.
type Tool struct {
	ws       workspace.Workspace
	generate generateFunc
}

// New builds the generate_image tool. router is required; nil leaves
// the tool un-wired so Execute fails with a clear internal error.
func New(router *route.Router, ws workspace.Workspace) (*Tool, error) {
	if ws == nil {
		return nil, errdefs.Validationf(
			"generate_image: workspace is required")
	}
	t := &Tool{ws: ws}
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

// Args is the generate_image tool input.
type Args struct {
	// Prompt is the text description of the image to generate.
	Prompt string `json:"prompt"`
	// Size is an optional WxH output size, e.g. "1024x1024".
	Size string `json:"size,omitempty"`
	// OutputFormat is an optional format: png, jpeg, or webp.
	OutputFormat string `json:"output_format,omitempty"`
}

// Definition describes the generate_image tool.
func (t *Tool) Definition() message.ToolDefinition {
	return message.DefineSchema(
		Name,
		"Generates raster images from a text prompt. The request is routed "+
			"through the configured inference router, which selects an "+
			"image-capable model (e.g. gpt-image) by output capability; if "+
			"the router has no such model, the call fails with guidance on "+
			"how to configure one. Generated images are written under "+
			"generated/ in the workspace, and the returned JSON lists the "+
			"workspace-relative paths plus the model that produced them.",
		message.ToolProperty("prompt", "string",
			"Text description of the image to generate (required)."),
		message.ToolProperty("size", "string",
			`Optional output size as WxH, e.g. "1024x1024". Provider defaults apply when omitted.`),
		message.ToolProperty("output_format", "string",
			"Optional output format: png, jpeg, or webp."),
	).Required("prompt").Build()
}

// Metadata reports the tool's execution metadata: it writes files and
// spends provider quota, and generation is unary but can take tens of
// seconds, so the tool bounds its own deadline.
func (t *Tool) Metadata() tool.ToolMeta {
	return tool.ToolMeta{
		MutatesState: true,
		SelfTimeout:  true,
	}
}

// Execute routes one text-to-image request through the router and
// persists every generated image under generated/ in the workspace.
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
	intent := &inference.ImageIntent{}
	if args.Size != "" {
		width, height, err := parseSize(args.Size)
		if err != nil {
			return "", errdefs.Validationf("%s: %v", Name, err)
		}
		intent.Size = &media.ImageSize{Width: width, Height: height}
	}
	if args.OutputFormat != "" {
		format := media.ImageFormat(strings.ToLower(args.OutputFormat))
		switch format {
		case media.ImageFormatPNG, media.ImageFormatJPEG, media.ImageFormatWebP:
			intent.OutputFormat = format
		default:
			return "", errdefs.Validationf(
				"%s: output_format must be png, jpeg, or webp", Name)
		}
	}

	req := inference.GenerateRequest{
		Input: inference.GenerateInput{
			Role: inference.InputRoleUser,
			Content: inference.InputContent{
				Content: message.Content{
					Parts: []message.Part{message.TextPart{Text: args.Prompt}},
				},
			},
		},
	}
	req.Input.Content.Intent.Image = intent

	resp, trace, err := t.generate(ctx, req)
	if err != nil {
		if route.IsKind(err, route.NoRoute) {
			return "", fmt.Errorf(
				"%s: the router has no model that supports image output; "+
					"add an image-capable model (e.g. openai/gpt-image-2) to "+
					"resources.router.settings.generate in "+
					"~/.opencraft/config/opencraft.yaml and set the provider "+
					"key on the settings page: %w", Name, err)
		}
		return "", err
	}

	paths, err := t.saveImages(ctx, resp)
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(map[string]any{
		"paths": paths,
		"count": len(paths),
		"model": modelLabel(trace.Executed.ID),
		"hint":  "Images are workspace-relative; open or reference them by path.",
	})
	if err != nil {
		return "", errdefs.Internalf(
			"%s: encode result: %v", Name, err)
	}
	return string(payload), nil
}

// saveImages writes every inline image part of the response under
// generated/ and returns the workspace-relative paths.
func (t *Tool) saveImages(
	ctx context.Context,
	resp inference.GenerateResponse,
) ([]string, error) {
	var paths []string
	for index, part := range resp.Message.Content.Parts {
		img, ok := part.(message.ImagePart)
		if !ok {
			continue
		}
		if img.Source.Kind() != media.SourceInline {
			return nil, errdefs.Internalf(
				"%s: generated image %d is not inline", Name, index)
		}
		path := OutDir + "/image-" + xid.New().String() +
			extensionFor(img.Source.BaseMediaType())
		if err := t.ws.Write(ctx, path, img.Source.Bytes()); err != nil {
			return nil, err
		}
		paths = append(paths, path)
	}
	if len(paths) == 0 {
		return nil, errdefs.Internalf(
			"%s: router response contained no image parts", Name)
	}
	return paths, nil
}

// parseSize parses a "WxH" size into positive integers.
func parseSize(raw string) (int, int, error) {
	width, height, ok := strings.Cut(
		strings.ToLower(strings.TrimSpace(raw)), "x")
	if !ok {
		return 0, 0, fmt.Errorf(
			"size must be WxH, e.g. \"1024x1024\"")
	}
	w, errW := strconv.Atoi(width)
	h, errH := strconv.Atoi(height)
	if errW != nil || errH != nil || w <= 0 || h <= 0 {
		return 0, 0, fmt.Errorf(
			"size must be two positive integers in WxH form")
	}
	return w, h, nil
}

// extensionFor maps a media type to a file extension, defaulting to
// .png for anything unrecognized.
func extensionFor(mediaType string) string {
	switch mediaType {
	case "image/jpeg":
		return ".jpg"
	case "image/webp":
		return ".webp"
	default:
		return ".png"
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
