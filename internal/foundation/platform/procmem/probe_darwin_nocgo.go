//go:build darwin && !cgo

package procmem

// Snapshot has no C implementation to call in a no-cgo build; the series
// is simply absent there. The desktop shell is built with cgo, so this is
// the CLI and test-only path.
func Snapshot() (Family, error) {
	return Family{}, ErrUnsupported
}
