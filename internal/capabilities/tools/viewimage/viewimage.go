// Package viewimage provides the view_image tool: it turns one
// workspace image file into a multimodal tool result — a short caption
// plus the image itself — so a vision model can look at a screenshot,
// a diagram, or a rendered chart instead of reading its bytes.
//
// The tool is deliberately separate from read_file: reading text and
// looking at pixels have different costs (an image part is replayed in
// every later turn's context) and different limits, so the model asks
// for one or the other explicitly. read_file keeps answering with text
// for every file, including images.
package viewimage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/message/media"
	"github.com/GizClaw/flowcraft/core/tool"
	"github.com/GizClaw/flowcraft/core/workspace"

	"github.com/GizClaw/opencraft/internal/foundation/utils/imageutil"
)

// Name is the canonical view_image tool name.
const Name = "view_image"

// maxSourceBytes caps the file the tool is willing to read. It matches
// the inline-image budget the attachment path uses, so "viewable" and
// "attachable" agree.
const maxSourceBytes = imageutil.MaxInlineImageBytes

// Defaults for the downscale targets; deployments can move them
// through the tool settings.
const (
	defaultMaxEdge  = imageutil.DefaultPromptImageEdge
	defaultMaxBytes = imageutil.DefaultPromptImageBytes
)

// Settings configures the prompt-side downscale targets.
type Settings struct {
	// MaxEdge is the longest-edge target in pixels.
	MaxEdge int `json:"max_edge,omitempty"`
	// MaxBytes is the encoded-size target. It should stay at or below
	// the tool-result part budget, or the middleware drops the image
	// before the model sees it.
	MaxBytes int `json:"max_bytes,omitempty"`
}

// Tool implements tool.Tool.
type Tool struct {
	ws       workspace.Workspace
	maxEdge  int
	maxBytes int
}

// New builds the view_image tool. ws is required.
func New(ws workspace.Workspace, settings Settings) (*Tool, error) {
	if ws == nil {
		return nil, errdefs.Validationf("%s: workspace is required", Name)
	}
	if settings.MaxEdge <= 0 {
		settings.MaxEdge = defaultMaxEdge
	}
	if settings.MaxBytes <= 0 {
		settings.MaxBytes = defaultMaxBytes
	}
	return &Tool{
		ws:       ws,
		maxEdge:  settings.MaxEdge,
		maxBytes: settings.MaxBytes,
	}, nil
}

var _ tool.Tool = (*Tool)(nil)

// Args is the view_image tool input.
type Args struct {
	// Path is the workspace-relative image path.
	Path string `json:"path"`
}

// Definition describes the tool.
func (*Tool) Definition() message.ToolDefinition {
	return message.DefineSchema(
		Name,
		"Loads a local image file from the workspace and returns it as "+
			"an image the model can see. Use this for screenshots, "+
			"diagrams, charts and photos; use read_file for text. Large "+
			"images are downscaled to fit the prompt budget, and only "+
			"formats the model's provider accepts as images are "+
			"supported (JPEG, PNG, GIF, TIFF, BMP).",
		message.ToolProperty("path", "string",
			"Workspace-relative path to the image file (required)."),
	).Required("path").Build()
}

// Metadata reports the tool's execution metadata: it only reads.
func (*Tool) Metadata() tool.ToolMeta { return tool.ToolMeta{} }

// Execute loads one image and returns it as an image part plus a text
// caption carrying the path and the dimensions that were encoded.
func (t *Tool) Execute(
	ctx context.Context, arguments string,
) (message.Content, error) {
	var args Args
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return message.Content{}, errdefs.Validationf(
			"%s: parse arguments: %v", Name, err,
		)
	}
	if args.Path == "" {
		return message.Content{}, errdefs.Validationf(
			"%s: path is required", Name,
		)
	}
	data, err := readSource(ctx, t.ws, args.Path)
	if err != nil {
		return message.Content{}, fmt.Errorf(
			"%s: read %s: %w", Name, args.Path, err,
		)
	}
	encoded, width, height, err := imageutil.DownscaleToJPEG(
		bytes.NewReader(data), t.maxEdge, t.maxBytes,
	)
	if err != nil {
		return message.Content{}, errdefs.Validationf(
			"%s: %s is not a supported image: %v", Name, args.Path, err,
		)
	}
	source, err := media.NewImageBytes(encoded, "image/jpeg")
	if err != nil {
		return message.Content{}, errdefs.Internalf(
			"%s: build image source: %v", Name, err,
		)
	}
	return message.Content{Parts: []message.Part{
		message.TextPart{Text: fmt.Sprintf(
			"view_image: %s (%dx%d, %d bytes)",
			args.Path, width, height, len(encoded),
		)},
		message.ImagePart{Source: source},
	}}, nil
}

// readSource reads one workspace file under the per-image cap. A
// workspace that supports bounded reads fails an oversized file instead
// of materializing it; other workspaces fall back to a full read.
func readSource(
	ctx context.Context, ws workspace.Workspace, path string,
) ([]byte, error) {
	if lr, ok := ws.(workspace.LimitedReader); ok {
		data, err := lr.ReadLimited(ctx, path, int64(maxSourceBytes))
		if err != nil {
			return nil, err
		}
		if len(data) > maxSourceBytes {
			// Defensive: the interface rejects oversized files, but a
			// backend that returns more than it was asked for must not
			// reach the decoder either.
			return nil, errdefs.Validationf(
				"%s: %s is over the %d-byte limit",
				Name, path, maxSourceBytes,
			)
		}
		return data, nil
	}
	data, err := ws.Read(ctx, path)
	if err != nil {
		return nil, err
	}
	if len(data) > maxSourceBytes {
		return nil, errdefs.Validationf(
			"%s: %s is %d bytes, over the %d-byte limit",
			Name, path, len(data), maxSourceBytes,
		)
	}
	return data, nil
}
