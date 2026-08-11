// Command opencraft is the opencraft coding agent entrypoint. It loads a
// deploy document (opencraft.yaml) and assembles the runtime through
// flowcraft's config-driven deploy machinery.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/GizClaw/flowcraft/sdk/agent"
	"github.com/GizClaw/flowcraft/sdk/message"
	sdkdeploy "github.com/GizClaw/flowcraft/sdkx/deploy"
	runtimecore "github.com/GizClaw/flowcraft/sdkx/runtime"
	sessions "github.com/GizClaw/flowcraft/sdkx/runtime/session"

	app "github.com/GizClaw/opencraft/internal/app"
	"github.com/GizClaw/opencraft/internal/config"
)

func main() {
	configPath := flag.String("config", "", "deploy document path (default: embedded, overridable by ~/.opencraft/config/opencraft.yaml)")
	flag.Parse()
	_ = app.LoadDotEnv(".env")
	if dir, err := app.UserDataDir(); err == nil {
		_ = app.LoadDotEnv(filepath.Join(dir, ".env"))
	}

	data, configBase, err := resolveDocument(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "opencraft: read config: %v\n", err)
		os.Exit(1)
	}
	doc, err := sdkdeploy.Parse(data)
	if err != nil {
		fmt.Fprintf(os.Stderr, "opencraft: parse config: %v\n", err)
		os.Exit(1)
	}
	rt, err := app.BuildRuntime(context.Background(), doc,
		app.WithConfigBase(configBase))
	if err != nil {
		fmt.Fprintf(os.Stderr, "opencraft: assemble runtime: %v\n", err)
		os.Exit(1)
	}
	defer func() {
		if err := rt.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "opencraft: close runtime: %v\n", err)
		}
	}()

	switch flag.Arg(0) {
	case "run":
		run(rt, flag.Arg(1))
	case "":
		fmt.Println("opencraft runtime ready (config-driven assembly)")
	default:
		fmt.Fprintf(os.Stderr, "opencraft: unknown command %q (run)\n", flag.Arg(0))
		os.Exit(2)
	}
}

// resolveDocument reads the deploy document: an explicit -config path
// wins; otherwise it seeds the user-facing assets into
// ~/.opencraft/config/ and uses the embedded opencraft.yaml (which
// references the seeded inference.yaml and embedded graph).
func resolveDocument(explicit string) ([]byte, string, error) {
	if explicit != "" {
		data, err := os.ReadFile(explicit)
		return data, filepath.Dir(explicit), err
	}
	dir, err := app.EnsureUserConfig()
	if err != nil {
		return nil, "", err
	}
	data, err := config.EmbeddedOpenCraft()
	return data, dir, err
}

func run(rt *runtimecore.Runtime, text string) {
	if text == "" {
		fmt.Fprintln(os.Stderr, "opencraft: run requires a message")
		os.Exit(2)
	}
	ctx := context.Background()
	key := sessions.Key{AgentID: "assistant", ContextID: "cli"}
	lease, err := rt.Sessions().Open(ctx, key)
	if err != nil {
		fmt.Fprintf(os.Stderr, "opencraft: open session: %v\n", err)
		os.Exit(1)
	}
	defer lease.Close()

	turn, err := lease.Session().Start(ctx, agent.Request{
		ContextID: key.ContextID,
		Message:   message.NewTextMessage(message.RoleUser, text),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "opencraft: start turn: %v\n", err)
		os.Exit(1)
	}
	res, err := turn.Wait(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "opencraft: turn: %v\n", err)
		os.Exit(1)
	}
	if res.Status != agent.StatusCompleted {
		if res.Err != nil {
			fmt.Fprintf(os.Stderr, "opencraft: turn failed: %v\n", res.Err)
		} else {
			fmt.Fprintf(os.Stderr, "opencraft: turn status=%s\n", res.Status)
		}
		os.Exit(1)
	}
	for _, msg := range res.Messages {
		if msg.Role == message.RoleAssistant {
			fmt.Println(msg.Content.Text())
		}
	}
}
