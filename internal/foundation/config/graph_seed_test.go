package config

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/GizClaw/flowcraft/core/resource"
	yamlv4 "go.yaml.in/yaml/v4"
)

// TestUserConfigAndGraphNotSeeded verifies EnsureUserConfig creates no
// config documents (the first-run wizard owns the user layer) and the
// default graph stays in the binary: neither the graph nor its node
// sources are seeded, and the embedded graph definition resolves its
// node sources through the same embed FS at load time.
func TestUserConfigAndGraphNotSeeded(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	// The hermetic-home layout: one root, so the app home and the state
	// root coincide exactly like the historical single-root callers.
	appHome := filepath.Join(dir, ".opencraft")
	cfgDir, err := EnsureUserConfig(appHome, appHome)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"opencraft.yaml"} {
		target := filepath.Join(cfgDir, filepath.FromSlash(name))
		if _, err := os.Stat(target); err == nil {
			t.Fatalf("config document %s must not be seeded (wizard-owned)", name)
		} else if !os.IsNotExist(err) {
			t.Fatalf("stat %s: %v", name, err)
		}
	}
	for _, name := range []string{
		"graphs/assistant.yaml",
		"graphs/nodes/world.js",
		"graphs/nodes/compact.js",
		"graphs/nodes/steer.js",
	} {
		if _, err := os.Stat(filepath.Join(cfgDir, filepath.FromSlash(name))); err == nil {
			t.Fatalf("default graph asset %s must not be seeded to the user dir", name)
		} else if !os.IsNotExist(err) {
			t.Fatalf("stat %s: %v", name, err)
		}
	}

	loader := resource.NewLoader(
		resource.WithBaseDir(cfgDir),
		resource.WithEmbed(FS()),
	)
	for _, src := range []resource.Source{
		{Embed: "assets/graphs/assistant.yaml"},
		{Embed: "assets/graphs/nodes/world.js"},
		{Embed: "assets/graphs/nodes/compact.js"},
		{Embed: "assets/graphs/nodes/steer.js"},
	} {
		data, err := loader.Load(context.Background(), src)
		if err != nil {
			t.Fatalf("load %v: %v", src, err)
		}
		if len(bytes.TrimSpace(data)) == 0 {
			t.Fatalf("load %v: empty", src)
		}
	}

	// The embedded agents layer must reference the graph as an embed
	// source so the binary copy is the one used at runtime, and the
	// graph's node sources must do the same. The base document keeps
	// the version + core resources and loads as the first layer.
	base, err := EmbeddedOpenCraft()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(base, []byte("version: v1")) {
		t.Fatal("embedded opencraft.yaml base layer is missing the document version")
	}
	agents, err := loader.Load(context.Background(),
		resource.Source{Embed: "assets/agents.yaml"})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(agents, []byte("embed: assets/graphs/assistant.yaml")) {
		t.Fatal("embedded agents.yaml does not reference the graph via embed source")
	}
	graph, err := loader.Load(context.Background(),
		resource.Source{Embed: "assets/graphs/assistant.yaml"})
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{
		"embed: assets/graphs/nodes/world.js",
		"embed: assets/graphs/nodes/compact.js",
		"embed: assets/graphs/nodes/steer.js",
	} {
		if !bytes.Contains(graph, []byte(ref)) {
			t.Fatalf("embedded graph does not reference %s via embed source", ref)
		}
	}
}

// TestAssistantGraphDiscardsFailedStreams guards the default graph's
// stream failure policy: a streamed inference that fails mid-flight
// (provider error or truncated stream) must discard its buffered
// partial text so a user retry never replays half an assistant
// message. Interrupts keep the default commit_partial behavior so
// cancelled output is still available to the archive observer.
func TestAssistantGraphDiscardsFailedStreams(t *testing.T) {
	type failurePolicy struct {
		OnError     string `yaml:"on_error"`
		OnInterrupt string `yaml:"on_interrupt"`
	}
	type nodeSpec struct {
		ID     string `yaml:"id"`
		Type   string `yaml:"type"`
		Config struct {
			Stream              bool           `yaml:"stream"`
			StreamFailurePolicy *failurePolicy `yaml:"stream_failure_policy"`
		} `yaml:"config"`
	}
	var graph struct {
		Nodes []nodeSpec `yaml:"nodes"`
	}
	data, err := FS().ReadFile("assets/graphs/assistant.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err := yamlv4.Unmarshal(data, &graph); err != nil {
		t.Fatalf("parse assistant.yaml: %v", err)
	}
	var llm *nodeSpec
	for i := range graph.Nodes {
		if graph.Nodes[i].ID == "llm" {
			llm = &graph.Nodes[i]
			break
		}
	}
	if llm == nil || llm.Type != "inference" {
		t.Fatalf("assistant graph has no inference node id=llm")
	}
	if !llm.Config.Stream {
		t.Fatal("llm node must stream; the failure policy only applies to streamed calls")
	}
	if llm.Config.StreamFailurePolicy == nil {
		t.Fatal("llm node must set stream_failure_policy")
	}
	if got := llm.Config.StreamFailurePolicy.OnError; got != "discard" {
		t.Fatalf("stream_failure_policy.on_error = %q, want discard", got)
	}
	if got := llm.Config.StreamFailurePolicy.OnInterrupt; got != "" {
		t.Fatalf("stream_failure_policy.on_interrupt = %q, want default (unset)", got)
	}
}

// TestAssistantAgentLiftsIterationGuard guards the default agent's
// budgets: the graph's node-routing guard is lifted with an explicit 0
// (an absent key keeps flowcraft's default 100, which ordinary tool
// work exhausts) and policy.run_timeout bounds the whole run instead,
// including revise attempts that restart the per-Execute timeout.
func TestAssistantAgentLiftsIterationGuard(t *testing.T) {
	type buildSpec struct {
		MaxIterations *int `yaml:"max_iterations"`
	}
	type assistantSpec struct {
		Engine struct {
			Settings struct {
				Build buildSpec `yaml:"build"`
			} `yaml:"settings"`
		} `yaml:"engine"`
		Policy struct {
			RunTimeout string `yaml:"run_timeout"`
		} `yaml:"policy"`
	}
	var doc struct {
		Agents map[string]assistantSpec `yaml:"agents"`
	}
	data, err := FS().ReadFile("assets/agents.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err := yamlv4.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse agents.yaml: %v", err)
	}
	assistant, ok := doc.Agents["assistant"]
	if !ok {
		t.Fatal("embedded agents.yaml declares no assistant agent")
	}
	build := assistant.Engine.Settings.Build
	if build.MaxIterations == nil {
		t.Fatal("build.max_iterations must be explicit: an absent key keeps flowcraft's default 100")
	}
	if *build.MaxIterations != 0 {
		t.Fatalf("build.max_iterations = %d, want 0 (unlimited)", *build.MaxIterations)
	}
	if got := assistant.Policy.RunTimeout; got != "2h" {
		t.Fatalf("policy.run_timeout = %q, want 2h once the iteration guard is lifted", got)
	}
}
