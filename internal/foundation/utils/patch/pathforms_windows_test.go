//go:build windows

package patch

import "testing"

// TestValidatePathWindowsForms covers the spellings only Windows reads
// as absolute, which an all-platform test cannot assert: a drive path is
// absolute to filepath.IsAbs even though its slash-folded form does not
// start with "/". "\x" is root-relative there, and absolute in the
// workspace namespace once folded, so it is refused too.
//
// This file is why the Windows lane runs this package: on any other host
// C:/escape.txt is an ordinary relative path, so only this run can tell
// whether the drive forms are still refused.
func TestValidatePathWindowsForms(t *testing.T) {
	for _, p := range []string{
		`C:\escape.txt`,
		`C:/escape.txt`,
		`\escape.txt`,
		`..\escape.txt`,
	} {
		if err := validatePath(p); err == nil {
			t.Errorf("validatePath(%q) = nil, want rejection", p)
		}
	}
	if err := validatePath(`src\main.go`); err != nil {
		t.Errorf("validatePath(`src\\main.go`) = %v, want accept", err)
	}
}
