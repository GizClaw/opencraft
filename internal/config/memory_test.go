package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMemorySettingsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	got, err := LoadMemory(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != defaultMemorySettings() {
		t.Fatalf("missing layer defaults = %+v, want %+v", got, defaultMemorySettings())
	}

	settings := MemorySettings{
		MaxRawMessages:    20,
		PreserveRecent:    2,
		MaxSummaryBytes:   2048,
		ReplayFullHistory: true,
	}
	if err := WriteMemory(dir, settings); err != nil {
		t.Fatal(err)
	}
	got, err = LoadMemory(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != settings {
		t.Fatalf("after write = %+v, want %+v", got, settings)
	}

	// Full save with the switch off persists false (never dropped).
	settings.ReplayFullHistory = false
	if err := WriteMemory(dir, settings); err != nil {
		t.Fatal(err)
	}
	got, err = LoadMemory(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != settings {
		t.Fatalf("after switch-off write = %+v, want %+v", got, settings)
	}
}

func TestWriteMemoryPreservesOtherResources(t *testing.T) {
	dir := t.TempDir()
	if err := WriteMemory(dir, MemorySettings{MaxRawMessages: 18}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.ReadFile(filepath.Join(dir, "opencraft.yaml")); err != nil {
		t.Fatal(err)
	}
	if err := WriteMCP(dir, []MCPServer{
		{Name: "x", Transport: "stdio", Command: "npx"},
	}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "opencraft.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "max_raw_messages") ||
		!strings.Contains(string(data), "tool.mcp") {
		t.Fatalf("memory settings lost after inference write:\n%s", data)
	}
	got, err := LoadMemory(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.MaxRawMessages != 18 {
		t.Fatalf("memory settings clobbered: %+v", got)
	}
}
