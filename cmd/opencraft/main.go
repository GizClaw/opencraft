// Command opencraft is the opencraft coding agent entrypoint. It loads a
// deploy document (opencraft.yaml) and assembles the runtime through
// flowcraft's config-driven deploy machinery.
package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"

	"github.com/GizClaw/flowcraft/sdk/agent"
	"github.com/GizClaw/flowcraft/sdk/message"
	sdkdeploy "github.com/GizClaw/flowcraft/sdkx/deploy"
	sessions "github.com/GizClaw/flowcraft/sdkx/runtime/session"

	app "github.com/GizClaw/opencraft/internal/app"
	"github.com/GizClaw/opencraft/internal/config"
	"github.com/GizClaw/opencraft/internal/execd"
)

func main() {
	configPath := flag.String("config", "", "deploy document path (default: embedded, overridable by ~/.opencraft/config/opencraft.yaml)")
	flag.Parse()
	_ = app.LoadDotEnv(".env")
	if dir, err := config.UserDataDir(); err == nil {
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
	// The execd subcommand is self-forked by the main process and
	// must not build a full runtime (it only serves process sessions).
	if flag.Arg(0) == "execd" {
		runExecServer()
		return
	}
	rtc, err := app.NewRuntimeController(context.Background(), doc,
		app.WithConfigBase(configBase))
	if err != nil {
		fmt.Fprintf(os.Stderr, "opencraft: assemble runtime: %v\n", err)
		os.Exit(1)
	}
	defer func() {
		if err := rtc.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "opencraft: close runtime: %v\n", err)
		}
	}()

	switch flag.Arg(0) {
	case "run":
		run(rtc, flag.Arg(1))
	case "":
		fmt.Println("opencraft runtime ready (config-driven assembly)")
	default:
		fmt.Fprintf(os.Stderr, "opencraft: unknown command %q (run)\n", flag.Arg(0))
		os.Exit(2)
	}
}

// runExecServer serves the exec JSON-RPC protocol on stdio or a unix
// socket. It is the child mode for opencraft's self-forked execd.
func runExecServer() {
	fs := flag.NewFlagSet("execd", flag.ExitOnError)
	listen := fs.String("listen", "",
		"unix socket path to listen on (empty: serve on stdio)")
	workDir := fs.String("workdir", "", "working directory (default: current)")
	if err := fs.Parse(flag.Args()[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "opencraft execd: %v\n", err)
		os.Exit(2)
	}

	ctx := context.Background()
	if _, err := config.EnsureUserConfig(); err != nil {
		fmt.Fprintf(os.Stderr, "opencraft execd: seed config: %v\n", err)
		os.Exit(1)
	}
	pm, err := app.SandboxProcessManager(ctx, *workDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "opencraft execd: %v\n", err)
		os.Exit(1)
	}

	if *listen == "" {
		if err := execd.New(pm, os.Stdin, os.Stdout).Serve(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "opencraft execd: %v\n", err)
			os.Exit(1)
		}
		return
	}
	listener, err := net.Listen("unix", *listen)
	if err != nil {
		fmt.Fprintf(os.Stderr, "opencraft execd: listen: %v\n", err)
		os.Exit(1)
	}
	defer listener.Close()
	_ = os.Chmod(*listen, 0o600)
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		go func() {
			defer conn.Close()
			_ = execd.New(pm, conn, conn).Serve(ctx)
		}()
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
	dir, err := config.EnsureUserConfig()
	if err != nil {
		return nil, "", err
	}
	data, err := config.EmbeddedOpenCraft()
	return data, dir, err
}

func run(rtc *app.RuntimeController, text string) {
	if text == "" {
		fmt.Fprintln(os.Stderr, "opencraft: run requires a message")
		os.Exit(2)
	}
	ctx := context.Background()
	rt := rtc.Runtime()
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
