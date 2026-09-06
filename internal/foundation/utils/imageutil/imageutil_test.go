package imageutil

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"

	"github.com/disintegration/imaging"
)

func TestNormalizeToJPEGFlattensTransparency(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 40, 30))
	for y := 0; y < 30; y++ {
		for x := 0; x < 40; x++ {
			src.SetNRGBA(x, y, color.NRGBA{R: 255, G: 0, B: 0, A: 255})
		}
	}
	// Leave one corner transparent so the alpha path is exercised.
	src.SetNRGBA(0, 0, color.NRGBA{})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, src); err != nil {
		t.Fatal(err)
	}

	data, err := NormalizeToJPEG(bytes.NewReader(encoded.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(data, []byte{0xff, 0xd8}) {
		t.Fatalf("output is not a JPEG: %x", data[:min(len(data), 4)])
	}
	img, err := imaging.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	b := img.Bounds()
	if b.Dx() != 40 || b.Dy() != 30 {
		t.Fatalf("dimensions = %dx%d, want 40x30", b.Dx(), b.Dy())
	}
	nrgba, ok := img.(*image.NRGBA)
	if ok && !nrgba.Opaque() {
		t.Fatal("jpeg output still carries transparency")
	}
}

func TestNormalizeToJPEGRejectsUnsupportedFormat(t *testing.T) {
	if _, err := NormalizeToJPEG(strings.NewReader("not an image")); err == nil {
		t.Fatal("unsupported input unexpectedly normalized")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
