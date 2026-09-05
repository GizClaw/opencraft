// Package profile reports compile-time product profiles selected by Go
// build tags. Regular builds keep every per-session sandbox mode; the
// yoloonly build (go build -tags yoloonly) pins every session to YOLO
// so read-only and workspace modes can never surface or execute.
package profile
