package execd

import (
	"bufio"
	"bytes"
	"testing"
)

// FuzzDecodeFrame pins that arbitrary bytes cannot panic the frame
// decoder or allocate unbounded memory.
func FuzzDecodeFrame(f *testing.F) {
	f.Add([]byte{0, 0, 0, 0})
	f.Add([]byte{0, 0, 0, 1, 0xFF})
	f.Add([]byte{0x7F, 0xFF, 0xFF, 0xFF})
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = decodeFrame(bufio.NewReader(bytes.NewReader(data)))
	})
}
