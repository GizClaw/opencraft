// Command telemetryplugin is a capability-plugin fixture for the
// desktop tests. It speaks the subprocess protocol, and on a
// "telemetry.probe" call it asks the host to install an OTLP export
// sink, then reports what the host answered.
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

	for in.Scan() {
		var req request
		if err := json.Unmarshal(in.Bytes(), &req); err != nil {
			continue
		}
		if req.Method != "telemetry.probe" {
			continue
		}
		var probe struct {
			Endpoint string            `json:"endpoint"`
			Headers  map[string]string `json:"headers,omitempty"`
			Insecure bool              `json:"insecure,omitempty"`
			// Exit makes the plugin die right after answering, which is
			// how tests cover the crash path.
			Exit bool `json:"exit,omitempty"`
		}
		if err := json.Unmarshal(req.Params, &probe); err != nil {
			fmt.Fprintln(os.Stderr, "probe params:", err)
			os.Exit(1)
		}
		write(map[string]any{
			"jsonrpc": "2.0",
			"id":      primitiveID,
			"method":  "telemetry.configure",
			"params": map[string]any{
				"endpoint": probe.Endpoint,
				"headers":  probe.Headers,
				"insecure": probe.Insecure,
			},
		})

		// The host answers the primitive on the same pipe; skip anything
		// else (there is nothing else while the host is blocked on our
		// reply) until the response for our own request arrives.
		result := map[string]any{"configured": false}
		for in.Scan() {
			var resp response
			if err := json.Unmarshal(in.Bytes(), &resp); err != nil {
				continue
			}
			if string(resp.ID) != fmt.Sprint(primitiveID) {
				continue
			}
			if resp.Error != nil {
				result["error"] = resp.Error.Message
			} else {
				result["configured"] = true
			}
			break
		}
		write(map[string]any{
			"jsonrpc": "2.0",
			"id":      json.RawMessage(req.ID),
			"result":  result,
		})
		if probe.Exit {
			return
		}
		// Stay alive: the host drops a plugin's export sink when its
		// process exits.
	}
}
