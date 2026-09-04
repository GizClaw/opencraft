package extract

import (
	"bytes"
	"testing"
)

func FuzzStripHiddenHTML(f *testing.F) {
	for _, seed := range []string{
		"<html><body><p>hi</p><script>x()</script></body></html>",
		"<div style=\"display:none\">hidden</div>",
		"",
		"<p>",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		out, err := StripHiddenHTML(bytes.NewReader([]byte(input)), DefaultSanitizeConfig)
		if err != nil {
			return
		}
		_ = CleanString(out, 1000)
	})
}
