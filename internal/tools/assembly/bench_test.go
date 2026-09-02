package assembly

import (
	"strings"
	"testing"
)

func benchAssembly(b *testing.B, settings string, content string) {
	asm := newAssembly(b, settings, stubSource{t: probeTool(content)})
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		runProbe(asm)
	}
}

func BenchmarkRedactChain(b *testing.B) {
	content := strings.Repeat("plain text ", 2048) + " sk-proj-1234567890abcdefghijklmnop"
	benchAssembly(b, `{
		"middlewares": {
			"redact": {
				"enabled": true,
				"rules": [
					{"pattern": "sk-proj-[A-Za-z0-9_-]{20,}"},
					{"pattern": "AKIA[0-9A-Z]{16}"}
				]
			}
		}
	}`, content)
}

func BenchmarkResultLimit(b *testing.B) {
	content := strings.Repeat("x", 100_000)
	benchAssembly(b, `{
		"middlewares": {
			"result_limit": {"max_chars": 32768}
		}
	}`, content)
}
