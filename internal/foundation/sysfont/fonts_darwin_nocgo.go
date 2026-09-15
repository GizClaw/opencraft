//go:build darwin && !cgo

package sysfont

// listSystemFonts reports no catalogue: reading the CoreText font store needs
// cgo, and a cgo-less build is not the desktop shell. The appearance settings
// fall back to typed family names.
func listSystemFonts() ([]string, error) {
	return nil, nil
}
