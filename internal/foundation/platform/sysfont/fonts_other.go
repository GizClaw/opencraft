//go:build !darwin && !windows && !linux

package sysfont

// listSystemFonts reports no catalogue on platforms the desktop shell does not
// target, so the picker falls back to typed family names.
func listSystemFonts() ([]string, error) {
	return nil, nil
}
