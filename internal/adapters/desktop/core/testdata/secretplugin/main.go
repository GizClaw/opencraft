// Command secretplugin is a kraft fixture for the desktop tests. It
// speaks the subprocess protocol, and on a "secret.probe" call it
// writes its token through the secret.set primitive, reads it back with
// secret.get, and reports what the host answered.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
)

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// primitiveID keeps the plugin→host request distinguishable from the
// host→plugin call it is answering.
const primitiveID = 99

func main() {
	in := bufio.NewScanner(os.Stdin)
	out := bufio.NewWriter(os.Stdout)
	write := func(v any) {
		data, err := json.Marshal(v)
		if err != nil {
			fmt.Fprintln(os.Stderr, "marshal:", err)
			os.Exit(1)
		}
		_, _ = out.Write(append(data, '\n'))
		_ = out.Flush()
	}
	write(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "handshake",
		"params":  map[string]any{"id": "plug", "protocol": 1},
	})

	// primitive sends one plugin→host request and returns the host's
	// result (or the error message on refusal).
	primitive := func(method string, params map[string]any) (map[string]any, string) {
		write(map[string]any{
			"jsonrpc": "2.0",
			"id":      primitiveID,
			"method":  method,
			"params":  params,
		})
		// The host answers on the same pipe; skip anything else (there
		// is nothing else while the host is blocked on our reply) until
		// the response for our own request arrives.
		for in.Scan() {
			var resp response
			if err := json.Unmarshal(in.Bytes(), &resp); err != nil {
				continue
			}
			if string(resp.ID) != fmt.Sprint(primitiveID) {
				continue
			}
			if resp.Error != nil {
				return nil, resp.Error.Message
			}
			result := map[string]any{}
			if err := json.Unmarshal(resp.Result, &result); err != nil {
				return nil, "decode result: " + err.Error()
			}
			return result, ""
		}
		return nil, "host closed the pipe"
	}

	for in.Scan() {
		var req request
		if err := json.Unmarshal(in.Bytes(), &req); err != nil {
			continue
		}
		if req.Method != "secret.probe" {
			continue
		}
		answer := map[string]any{"stored": false, "value": ""}
		_, errMsg := primitive("secret.set", map[string]any{
			"scope": "auth", "name": "plug/probe-token", "value": "probe-secret",
		})
		if errMsg != "" {
			answer["error"] = errMsg
		} else {
			result, getErr := primitive("secret.get", map[string]any{
				"scope": "auth", "name": "plug/probe-token",
			})
			if getErr != "" {
				answer["error"] = getErr
			} else {
				answer["stored"] = true
				answer["value"], _ = result["value"].(string)
			}
		}
		write(map[string]any{
			"jsonrpc": "2.0",
			"id":      json.RawMessage(req.ID),
			"result":  answer,
		})
	}
}
