package patch

import (
	"fmt"
	"strings"
	"testing"
)

func benchPatch(b *testing.B) string {
	var sb strings.Builder
	sb.WriteString("*** Begin Patch\n")
	for i := 0; i < 50; i++ {
		fmt.Fprintf(&sb, "*** Add File: file_%d.txt\n+line one\n+line two\n", i)
	}
	sb.WriteString("*** End Patch\n")
	return sb.String()
}

func BenchmarkParse(b *testing.B) {
	p := benchPatch(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Parse(p); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkParseAndRender(b *testing.B) {
	p := benchPatch(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Diff(p, nil); err != nil {
			b.Fatal(err)
		}
	}
}
