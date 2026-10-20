// Package jsonout encodes a tool result for the model.
//
// encoding/json escapes <, > and & by default. That rule protects an
// HTML embedding, and a tool result is not one: a markdown blockquote, a
// line of Go with &&, or a generics signature came back as \u003e,
// \u0026 and \u003c, and a model copying those lines into a patch wrote
// the escape instead of the character it means. Here the text fields are
// the file's own characters, and only the JSON syntax is escaped.
package jsonout

import (
	"bytes"
	"encoding/json"
	"strings"
)

// Marshal encodes v like encoding/json, with the string escapes cut back
// to what JSON syntax needs (quote, backslash, control characters) and
// no HTML escaping. The trailing newline encoding/json appends is not
// part of a result.
func Marshal(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return []byte(strings.TrimSuffix(buf.String(), "\n")), nil
}
