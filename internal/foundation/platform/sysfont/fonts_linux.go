//go:build linux

package sysfont

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// fcListTimeout bounds the fontconfig query so a wedged font cache cannot hang
// the settings page.
const fcListTimeout = 3 * time.Second

// listSystemFonts asks fontconfig for the installed families. Linux has no
// in-process font catalogue: fc-list is the distribution-agnostic answer, and
// a machine without fontconfig simply reports an empty picker.
func listSystemFonts() ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), fcListTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "fc-list", "--format", "%{family}\n").Output()
	if err != nil {
		return nil, fmt.Errorf("sysfont: fc-list: %w", err)
	}
	return parseFontconfigFamilies(string(out)), nil
}

// parseFontconfigFamilies reads fc-list's one-face-per-line output. A face
// carries its localized family names comma-separated, so every entry is a
// candidate family name.
func parseFontconfigFamilies(out string) []string {
	var families []string
	for _, line := range strings.Split(out, "\n") {
		for _, family := range strings.Split(line, ",") {
			if family = strings.TrimSpace(family); family != "" {
				families = append(families, family)
			}
		}
	}
	return families
}
