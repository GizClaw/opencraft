//go:build !darwin && !linux

package procmem

// Snapshot is not implemented on this platform. Windows would read the
// tree through Toolhelp32 and the footprint through GetProcessMemoryInfo;
// until a Windows build wants the series, the sampler drops it.
func Snapshot() (Family, error) {
	return Family{}, ErrUnsupported
}
