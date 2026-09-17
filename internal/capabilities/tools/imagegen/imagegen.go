// Package imagegen provides the generate_image tool: it turns a text
// prompt — optionally guided by reference images, a mask, and
// provider-side knobs — into raster images by routing an image-output
// inference request through the deployment router. No model is pinned
// here: the router's capability-aware selection picks an image-capable
// target (e.g. gpt-image) from the user's generate targets, and the
// returned inline image parts are written under generated/ in the
// workspace.
package imagegen

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/rs/xid"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/inference"
	"github.com/GizClaw/flowcraft/core/inference/route"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/message/media"
	"github.com/GizClaw/flowcraft/core/telemetry"
	"github.com/GizClaw/flowcraft/core/tool"
	"github.com/GizClaw/flowcraft/core/workspace"

	"github.com/GizClaw/opencraft/internal/foundation/utils/imageutil"
	"github.com/GizClaw/opencraft/internal/foundation/utils/inferenceext"
	"github.com/GizClaw/opencraft/internal/foundation/utils/wsread"
)

// Name is the canonical generate_image tool name.
const Name = "generate_image"

// OutDir is the workspace-relative directory generated images land in.
const OutDir = "generated"

// PreviewDir is the workspace-relative directory the progress previews
// of a streamed generation land in.
const PreviewDir = OutDir + "/previews"

// defaultTimeout bounds one call. Generation is one unary request from
// the model's point of view but can take minutes, and the tool is
// exempt from the generic tool timeout (SelfTimeout), so this deadline
// is the bound the tool owns.
const defaultTimeout = 10 * time.Minute

// maxReferenceImages caps the reference images one call may upload. It
// matches the images/edits ceiling of the OpenAI wire family; a
// provider that accepts fewer rejects the request in its own compiler.
const maxReferenceImages = 16

// maxPartialImages is the provider-side ceiling on streamed progress
// previews (the OpenAI wire family's partial_images).
const maxPartialImages = 3

// imageExtensionID is the provider-carried extension that models
// per-request image knobs (the image_options extension of the
// OpenAI-wire family and of the ByteDance driver). Which field a driver
// accepts is decided by the driver's own strict decoder: the tool
// probes the configured providers and attaches only what each one
// models, so this file carries no driver field table.
const imageExtensionID = "image_options"

// generateFunc is the unary generation entry; production wires the
// router, tests inject a fake.
type generateFunc func(
	ctx context.Context,
	req inference.GenerateRequest,
) (inference.GenerateResponse, route.Trace, error)

// streamFunc is the streaming entry, used when the caller asks for
// progress previews (the only execution shape that carries them).
type streamFunc func(
	ctx context.Context,
	req inference.GenerateRequest,
) (inference.GenerateStream, route.Trace, error)

// Tool generates images through the deployment router. It is safe for
// concurrent use: the router and workspace are shared, and no mutable
// state is kept per call.
type Tool struct {
	ws       workspace.Workspace
	generate generateFunc
	stream   streamFunc
	// extensions renders the provider-addressed image knob fields into
	// typed extensions. It is nil when no router is wired, in which case
	// the knobs that need a provider extension are rejected.
	extensions func(fields map[string]any) (inference.Extensions, error)
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
		t.stream = func(
			ctx context.Context,
			req inference.GenerateRequest,
		) (inference.GenerateStream, route.Trace, error) {
			return router.GenerateStream(ctx, req)
		}
		t.extensions = func(
			fields map[string]any,
		) (inference.Extensions, error) {
			return imageExtensions(router.Target(), fields)
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
	// Model is an optional router hint ("<provider-id>/<name>" or a
	// bare model name). It is honored only for a configured target that
	// can serve image output.
	Model string `json:"model,omitempty"`
	// Size is an optional WxH output size, e.g. "1024x1024".
	Size string `json:"size,omitempty"`
	// AspectRatio is an optional "W:H" ratio for providers that size by
	// ratio instead of pixels; it is mutually exclusive with Size.
	AspectRatio string `json:"aspect_ratio,omitempty"`
	// Count is the optional number of images to generate.
	Count *int `json:"count,omitempty"`
	// Seed fixes the provider's sampling seed where supported.
	Seed *int64 `json:"seed,omitempty"`
	// Quality is an optional generation quality tier: auto, low, medium,
	// high, xhigh, or max. Providers without the knob report it as
	// dropped.
	Quality string `json:"quality,omitempty"`
	// OutputFormat is an optional format: png, jpeg, or webp.
	OutputFormat string `json:"output_format,omitempty"`
	// ReferenceImages are workspace-relative image paths sent as
	// references (image-to-image / edit). The first one is the edit
	// target a mask applies to.
	ReferenceImages []string `json:"reference_images,omitempty"`
	// Mask is a workspace-relative PNG whose transparent pixels mark
	// the area to repaint in the first reference image.
	Mask string `json:"mask,omitempty"`
	// PartialImages requests up to maxPartialImages progress previews
	// streamed while the image is generated; the previews are saved
	// under PreviewDir.
	PartialImages *int `json:"partial_images,omitempty"`
}

// Definition describes the generate_image tool.
func (t *Tool) Definition() message.ToolDefinition {
	return message.DefineSchema(
		Name,
		"Generates raster images from a text prompt, optionally guided by "+
			"reference images (image-to-image) and a mask. The request is "+
			"routed through the configured inference router, which selects "+
			"an image-capable model by output capability; if the router has "+
			"no such model, the call fails with guidance on how to "+
			"configure one. A knob the selected provider does not support "+
			"is reported in the failure or as a dropped field rather than "+
			"silently ignored. Generated images are written under "+
			"generated/ in the workspace, and the returned JSON lists the "+
			"workspace-relative paths (plus previews when requested) and "+
			"the model that produced them.",
		message.ToolProperty("prompt", "string",
			"Text description of the image to generate (required)."),
		message.ToolProperty("model", "string",
			`Optional model hint, "<provider-id>/<model>" or a bare model `+
				"name. The router honors it only for a target that can "+
				"serve image output; otherwise the default route applies."),
		message.ToolProperty("size", "string",
			`Optional output size as WxH, e.g. "1024x1024". Mutually `+
				"exclusive with aspect_ratio; provider defaults apply when "+
				"both are omitted."),
		message.ToolProperty("aspect_ratio", "string",
			`Optional "W:H" ratio, e.g. "16:9", for providers that size by `+
				"ratio. Mutually exclusive with size; providers that need "+
				"explicit pixels reject it."),
		message.ToolProperty("count", "integer",
			"Optional number of images to generate (positive). Providers "+
				"cap it, e.g. at 9 or 10."),
		message.ToolProperty("seed", "integer",
			"Optional sampling seed for reproducible output, where the "+
				"provider supports one."),
		message.ToolEnumProperty("quality", "string",
			"Optional quality tier; providers without a native quality "+
				"knob report it as dropped.",
			"auto", "low", "medium", "high", "xhigh", "max"),
		message.ToolProperty("output_format", "string",
			"Optional output format: png, jpeg, or webp."),
		message.ToolArrayProperty("reference_images",
			"Optional workspace-relative image paths used as references "+
				"(image-to-image / edit), up to 16. The first one is the "+
				"edit target a mask applies to.",
			message.Items("string")),
		message.ToolProperty("mask", "string",
			"Optional workspace-relative PNG whose transparent pixels "+
				"mark the area to repaint in the first reference image. "+
				"Requires at least one reference image and a provider that "+
				"supports masks."),
		message.ToolProperty("partial_images", "integer",
			"Optional number of progress previews (0-3) streamed while "+
				"generating; previews are written under generated/previews/. "+
				"Requires a provider that supports previews."),
	).Required("prompt").Build()
}

// Metadata reports the tool's execution metadata: it writes files and
// spends provider quota. It declares SelfTimeout because generation can
// outlive the generic tool timeout; defaultTimeout is the deadline the
// tool owns instead.
func (t *Tool) Metadata() tool.ToolMeta {
	return tool.ToolMeta{
		MutatesState: true,
		SelfTimeout:  true,
	}
}

// Execute routes one image request through the router and persists
// every generated image under generated/ in the workspace. Execute
// implements tool.Tool. The tool result is a single text part; the tool
// has no multimodal output.
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
	ctx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()

	var (
		resp     inference.GenerateResponse
		trace    route.Trace
		previews []string
	)
	if previewCount(args) > 0 {
		resp, trace, previews, err = t.generateWithPreviews(ctx, req)
	} else {
		resp, trace, err = t.generate(ctx, req)
	}
	if err != nil {
		if route.IsKind(err, route.NoRoute) {
			return "", fmt.Errorf(
				"%s: the router has no model that supports image output; "+
					"add an image-capable model (e.g. openai/gpt-image-2) to "+
					"resources.router.settings.generate in "+
					"~/.opencraft/config/opencraft.yaml and set the provider "+
					"key on the settings page: %w", Name, err)
		}
		return "", inferenceext.Explain(err)
	}
	if err := inferenceext.Verify(Name, resp, req.Extensions); err != nil {
		return "", err
	}

	paths, err := t.saveImages(ctx, resp)
	if err != nil {
		return "", err
	}
	payload := map[string]any{
		"paths": paths,
		"count": len(paths),
		"model": inferenceext.Label(trace.Executed.ID),
		"hint":  "Images are workspace-relative; open or reference them by path.",
	}
	if len(previews) > 0 {
		payload["previews"] = previews
	}
	out, err := json.Marshal(payload)
	if err != nil {
		return "", errdefs.Internalf(
			"%s: encode result: %v", Name, err)
	}
	return string(out), nil
}

// request lowers validated arguments into the routed generate request:
// the prompt plus any reference images, the canonical image intent, the
// provider-addressed knobs, and the optional model hint.
func (t *Tool) request(
	ctx context.Context, args Args,
) (inference.GenerateRequest, error) {
	intent, err := imageIntent(args)
	if err != nil {
		return inference.GenerateRequest{}, err
	}
	parts := []message.Part{
		message.TextPart{Text: strings.TrimSpace(args.Prompt)},
	}
	if len(args.ReferenceImages) > maxReferenceImages {
		return inference.GenerateRequest{}, errdefs.Validationf(
			"%s: at most %d reference images are supported, got %d",
			Name, maxReferenceImages, len(args.ReferenceImages))
	}
	for _, path := range args.ReferenceImages {
		source, err := t.loadReferenceImage(ctx, path)
		if err != nil {
			return inference.GenerateRequest{}, err
		}
		parts = append(parts, message.ImagePart{Source: source})
	}
	fields := map[string]any{}
	if strings.TrimSpace(args.Mask) != "" {
		if len(args.ReferenceImages) == 0 {
			return inference.GenerateRequest{}, errdefs.Validationf(
				"%s: mask requires at least one reference image", Name)
		}
		mask, err := t.loadMask(ctx, args.Mask)
		if err != nil {
			return inference.GenerateRequest{}, err
		}
		fields["mask"] = mask
	}
	if count := previewCount(args); count > 0 {
		fields["partial_images"] = count
	}
	extensions, err := t.providerExtensions(fields)
	if err != nil {
		return inference.GenerateRequest{}, err
	}
	return inference.GenerateRequest{
		Input: inference.GenerateInput{
			Role: inference.InputRoleUser,
			Content: inference.InputContent{
				Content: message.Content{Parts: parts},
				Intent:  inference.Intent{Image: intent},
			},
		},
		Extensions: extensions,
		ModelHint:  strings.TrimSpace(args.Model),
	}, nil
}

// imageIntent validates the argument-level image knobs and lowers them
// into the canonical intent. Delivery is pinned to inline: the tool
// persists bytes, and a provider that defaults to URLs (MiniMax) would
// otherwise hand back a source the tool cannot save.
func imageIntent(args Args) (*inference.ImageIntent, error) {
	prompt := strings.TrimSpace(args.Prompt)
	if prompt == "" {
		return nil, errdefs.Validationf("%s: prompt is required", Name)
	}
	size := strings.TrimSpace(args.Size)
	ratio := strings.TrimSpace(args.AspectRatio)
	if size != "" && ratio != "" {
		return nil, errdefs.Validationf(
			"%s: size and aspect_ratio are mutually exclusive", Name)
	}
	intent := &inference.ImageIntent{Delivery: media.SourceInline}
	if size != "" {
		width, height, err := parseSize(size)
		if err != nil {
			return nil, errdefs.Validationf("%s: %v", Name, err)
		}
		intent.Size = &media.ImageSize{Width: width, Height: height}
	}
	if ratio != "" {
		value := media.AspectRatio(ratio)
		if err := value.Validate(); err != nil {
			return nil, errdefs.Validationf(
				"%s: aspect_ratio: %v", Name, err)
		}
		intent.AspectRatio = value
	}
	if args.Count != nil {
		if *args.Count <= 0 {
			return nil, errdefs.Validationf(
				"%s: count must be positive, got %d", Name, *args.Count)
		}
		intent.Count = args.Count
	}
	if args.Seed != nil {
		intent.Seed = args.Seed
	}
	if raw := strings.TrimSpace(args.Quality); raw != "" {
		quality := media.ImageQuality(strings.ToLower(raw))
		switch quality {
		case media.ImageQualityAuto, media.ImageQualityLow,
			media.ImageQualityMedium, media.ImageQualityHigh,
			media.ImageQualityXHigh, media.ImageQualityMax:
			intent.Quality = quality
		default:
			return nil, errdefs.Validationf(
				"%s: quality must be auto, low, medium, high, xhigh, "+
					"or max, got %q",
				Name, raw)
		}
	}
	if raw := strings.TrimSpace(args.OutputFormat); raw != "" {
		format := media.ImageFormat(strings.ToLower(raw))
		switch format {
		case media.ImageFormatPNG, media.ImageFormatJPEG, media.ImageFormatWebP:
			intent.OutputFormat = format
		default:
			return nil, errdefs.Validationf(
				"%s: output_format must be png, jpeg, or webp", Name)
		}
	}
	if args.PartialImages != nil {
		if *args.PartialImages < 0 || *args.PartialImages > maxPartialImages {
			return nil, errdefs.Validationf(
				"%s: partial_images must be between 0 and %d, got %d",
				Name, maxPartialImages, *args.PartialImages)
		}
	}
	return intent, nil
}

// previewCount reports the requested preview count; an absent or zero
// knob means the provider default (no previews) and keeps the unary
// execution shape.
func previewCount(args Args) int {
	if args.PartialImages == nil {
		return 0
	}
	return *args.PartialImages
}

// generateWithPreviews runs one request through the streaming shape —
// the only shape that carries interim previews — and saves every
// preview under PreviewDir. The accumulated stream result is the final
// response.
func (t *Tool) generateWithPreviews(
	ctx context.Context, req inference.GenerateRequest,
) (inference.GenerateResponse, route.Trace, []string, error) {
	if t.stream == nil {
		return inference.GenerateResponse{}, route.Trace{}, nil,
			errdefs.Internalf("%s: router is not wired", Name)
	}
	stream, trace, err := t.stream(ctx, req)
	if err != nil {
		return inference.GenerateResponse{}, trace, nil, err
	}
	defer func() {
		telemetry.WarnErr(ctx,
			Name+": close generate stream failed", stream.Close())
	}()
	var previews []string
	for {
		event, err := stream.Next(ctx)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return inference.GenerateResponse{}, trace, previews, err
		}
		delta, ok := event.Delta.(inference.ImagePartDelta)
		if !ok || !delta.Interim {
			continue
		}
		path, err := t.saveImagePart(ctx, delta.Part, PreviewDir, "preview")
		if err != nil {
			return inference.GenerateResponse{}, trace, previews,
				fmt.Errorf("%s: save preview: %w", Name, err)
		}
		previews = append(previews, path)
	}
	resp, err := stream.Result()
	if err != nil {
		return inference.GenerateResponse{}, trace, previews, err
	}
	return resp, trace, previews, nil
}

// providerExtensions renders the requested provider-addressed knobs.
// An empty field set needs no extension; a non-empty one fails loudly
// when no configured provider models it, so a knob the deployment
// cannot honor never disappears silently.
func (t *Tool) providerExtensions(
	fields map[string]any,
) (inference.Extensions, error) {
	if len(fields) == 0 {
		return nil, nil
	}
	if t.extensions == nil {
		return nil, errdefs.Internalf("%s: router is not wired", Name)
	}
	return t.extensions(fields)
}

// imageExtensions probes the configured providers for the requested
// image knobs and returns the typed extensions to attach.
func imageExtensions(
	assembly *inference.Assembly, fields map[string]any,
) (inference.Extensions, error) {
	return inferenceext.Probe(Name, assembly, imageExtensionID, fields)
}

// loadReferenceImage reads one workspace image and returns it as an
// inline source. Bytes already in a container the image APIs accept
// (PNG/JPEG/WebP) pass through unchanged so an edit keeps full
// fidelity; any other decodable format is re-encoded to a JPEG that
// fits the inline budget.
func (t *Tool) loadReferenceImage(
	ctx context.Context, path string,
) (media.ImageSource, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return media.ImageSource{}, errdefs.Validationf(
			"%s: reference image path is empty", Name)
	}
	data, err := wsread.Capped(ctx, t.ws, path, imageutil.MaxInlineImageBytes)
	if err != nil {
		return media.ImageSource{}, fmt.Errorf(
			"%s: read reference image %s: %w", Name, path, err)
	}
	mediaType := sniffImageMediaType(data)
	if mediaType == "" {
		converted, _, _, err := imageutil.DownscaleToJPEG(
			bytes.NewReader(data), 0, imageutil.MaxInlineImageBytes)
		if err != nil {
			return media.ImageSource{}, errdefs.Validationf(
				"%s: %s is not a supported image: %v", Name, path, err)
		}
		data, mediaType = converted, "image/jpeg"
	}
	source, err := media.NewImageBytes(data, mediaType)
	if err != nil {
		return media.ImageSource{}, errdefs.Internalf(
			"%s: build reference image source: %v", Name, err)
	}
	return source, nil
}

// loadMask reads the mask file verbatim: a mask must be a PNG whose
// transparency marks the edit area, so re-encoding it would change the
// mask.
func (t *Tool) loadMask(
	ctx context.Context, path string,
) (media.ImageSource, error) {
	path = strings.TrimSpace(path)
	data, err := wsread.Capped(ctx, t.ws, path, imageutil.MaxInlineImageBytes)
	if err != nil {
		return media.ImageSource{}, fmt.Errorf(
			"%s: read mask %s: %w", Name, path, err)
	}
	if sniffImageMediaType(data) != "image/png" {
		return media.ImageSource{}, errdefs.Validationf(
			"%s: mask %s must be a PNG image", Name, path)
	}
	source, err := media.NewImageBytes(data, "image/png")
	if err != nil {
		return media.ImageSource{}, errdefs.Internalf(
			"%s: build mask source: %v", Name, err)
	}
	return source, nil
}

// saveImages writes every inline image part of the response under
// OutDir and returns the workspace-relative paths.
func (t *Tool) saveImages(
	ctx context.Context, resp inference.GenerateResponse,
) ([]string, error) {
	var paths []string
	for index, part := range resp.Message.Content.Parts {
		if _, ok := part.(message.ImagePart); !ok {
			continue
		}
		path, err := t.saveImagePart(ctx, part, OutDir, "image")
		if err != nil {
			return nil, fmt.Errorf("%s: save image %d: %w", Name, index, err)
		}
		paths = append(paths, path)
	}
	if len(paths) == 0 {
		return nil, errdefs.Internalf(
			"%s: router response contained no image parts", Name)
	}
	return paths, nil
}

// saveImagePart writes one inline image part under dir and returns its
// workspace-relative path.
func (t *Tool) saveImagePart(
	ctx context.Context, part message.Part, dir, prefix string,
) (string, error) {
	img, ok := part.(message.ImagePart)
	if !ok {
		return "", errdefs.Internalf(
			"%s: response part %T is not an image", Name, part)
	}
	if img.Source.Kind() != media.SourceInline {
		return "", errdefs.Internalf(
			"%s: generated image is not inline; the tool requests inline "+
				"delivery", Name)
	}
	path := dir + "/" + prefix + "-" + xid.New().String() +
		extensionFor(img.Source.BaseMediaType())
	if err := t.ws.Write(ctx, path, img.Source.Bytes()); err != nil {
		return "", err
	}
	return path, nil
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

// sniffImageMediaType reports the container of data for the formats the
// image APIs accept inline, or "" when the bytes are something else
// (the caller then decodes and re-encodes them).
func sniffImageMediaType(data []byte) string {
	switch {
	case bytes.HasPrefix(data, []byte{
		0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n',
	}):
		return "image/png"
	case bytes.HasPrefix(data, []byte{0xFF, 0xD8, 0xFF}):
		return "image/jpeg"
	case len(data) >= 12 &&
		bytes.Equal(data[0:4], []byte("RIFF")) &&
		bytes.Equal(data[8:12], []byte("WEBP")):
		return "image/webp"
	default:
		return ""
	}
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
