//go:build linux

package sysfont

import (
	"slices"
	"testing"
)

func TestParseFontconfigFamilies(t *testing.T) {
	// fc-list prints one face per line, with the localized family names of
	// that face comma-separated.
	out := "DejaVu Sans,DejaVu Sans Book\n" +
		"Noto Sans CJK SC,Noto Sans CJK SC Regular\n" +
		"\n" +
		" Source Han Sans SC \n"
	got := parseFontconfigFamilies(out)
	want := []string{
		"DejaVu Sans",
		"DejaVu Sans Book",
		"Noto Sans CJK SC",
		"Noto Sans CJK SC Regular",
		"Source Han Sans SC",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("parseFontconfigFamilies() = %v, want %v", got, want)
	}
}
