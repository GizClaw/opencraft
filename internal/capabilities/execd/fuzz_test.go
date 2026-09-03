package execd

import (
	"encoding/json"
	"testing"
)

func FuzzRPCRequest(f *testing.F) {
	for _, seed := range []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"id":null,"method":"process/start"}`,
		"garbage",
		"",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		var req RPCRequest
		_ = json.Unmarshal([]byte(input), &req)
	})
}
