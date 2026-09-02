package desktop

// Thin wails binding over the capability subprocess runtime. The host
// only routes method-name + JSON payloads; it never interprets domain
// semantics (see internal/plugins/runtime).

import (
	"encoding/json"
	"errors"
	"strings"
)

// PluginInvoke forwards one method call to a plugin's capability
// subprocess. args and result are JSON strings.
func (a *App) PluginInvoke(pluginID, method, args string) (string, error) {
	if a.cap == nil {
		return "", errors.New("capability runtime is not ready")
	}
	var params any
	if strings.TrimSpace(args) != "" {
		if err := json.Unmarshal([]byte(args), &params); err != nil {
			return "", errors.New("capability: invalid args JSON")
		}
	}
	res, err := a.cap.Invoke(a.appContext(), pluginID, method, params)
	if err != nil {
		return "", err
	}
	return string(res), nil
}
