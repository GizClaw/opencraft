// Package imageutil normalizes user-attached images before they are
// persisted and inlined into model prompts: EXIF orientation is
// applied, transparency is flattened onto white, and the result is
// re-encoded as JPEG so prompt bytes stay predictable regardless of
// the source format.
package imageutil

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"io"
	"os"

	"github.com/disintegration/imaging"
)

// MaxInlineImageBytes is the shared per-image prompt inline budget.
// Preview data URLs, host-side attachment persistence, the media
// prepare hook, and the sessions media kind all enforce the same cap
// so "attaches today" stays true all the way into the model prompt.
const MaxInlineImageBytes = 10 << 20

// MaxDecodePixels bounds the pixel budget of images that are fully
// decoded for normalization. Decoding a huge-but-compressed image
// (a decompression bomb) would otherwise allocate hundreds of MB
// per working buffer in the desktop process.
const MaxDecodePixels = 40_000_000

// JPEGQuality is the quality used for normalized attachment images.
const JPEGQuality = 90

// NormalizeToJPEG decodes r (JPEG/PNG/GIF/TIFF/BMP with EXIF
// orientation applied), flattens transparency onto white, and
// re-encodes the result as a JPEG at JPEGQuality. Formats the decoder
// does not understand (for example WebP/AVIF) return an error so
// callers can fall back to the original bytes. The pixel budget guard
// lives in NormalizeFileToJPEG, which is the production entry point;
// this reader variant is used by tests and callers that already
// validated the input.
func NormalizeToJPEG(r io.Reader) ([]byte, error) {
	img, err := imaging.Decode(r, imaging.AutoOrientation(true))
	if err != nil {
		return nil, err
	}
	img = flattenAlpha(img)
	var out bytes.Buffer
	if err := imaging.Encode(
		&out, img, imaging.JPEG, imaging.JPEGQuality(JPEGQuality),
	); err != nil {
		return nil, fmt.Errorf("imageutil: encode jpeg: %w", err)
	}
	return out.Bytes(), nil
}

// NormalizeFileToJPEG is NormalizeToJPEG for a local path. It checks
// the pixel budget before decoding, so a sparse image with enormous
// dimensions is rejected without a full-size allocation.
func NormalizeFileToJPEG(path string) (out []byte, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := f.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return nil, err
	}
	pixels := int64(cfg.Width) * int64(cfg.Height)
	if cfg.Width <= 0 || cfg.Height <= 0 || pixels > MaxDecodePixels {
		return nil, fmt.Errorf(
			"imageutil: %s is %dx%d and exceeds the %d-pixel decode limit",
			path, cfg.Width, cfg.Height, MaxDecodePixels)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("imageutil: rewind %s: %w", path, err)
	}
	out, err = NormalizeToJPEG(f)
	return out, err
}

// flattenAlpha replaces transparency with white. JPEG cannot carry an
// alpha channel, and leaving transparent pixels black turns PNG icons
// and screenshots illegible.
func flattenAlpha(src image.Image) image.Image {
	if alphaFree(src) {
		return src
	}
	b := src.Bounds()
	canvas := imaging.New(b.Dx(), b.Dy(), color.White)
	return imaging.Overlay(canvas, src, image.Pt(-b.Min.X, -b.Min.Y), 1)
}

// alphaFree reports whether src is known not to carry transparency, so
// flattenAlpha can skip the extra full-size compositing pass.
func alphaFree(src image.Image) bool {
	switch m := src.(type) {
	case *image.YCbCr, *image.CMYK, *image.Gray, *image.Gray16:
		return true
	case *image.RGBA:
		return m.Opaque()
	case *image.NRGBA:
		return m.Opaque()
	case *image.Paletted:
		for _, c := range m.Palette {
			if _, _, _, a := c.RGBA(); a != 0xffff {
				return false
			}
		}
		return true
	default:
		// Unknown concrete type: composite conservatively.
		return false
	}
}
