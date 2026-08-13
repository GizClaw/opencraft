// Command opencraft is the opencraft coding agent entrypoint. It loads a
// deploy document (opencraft.yaml) and assembles the runtime through
// flowcraft's config-driven deploy machinery.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/message"
	sessions "github.com/GizClaw/flowcraft/core/runtime/session"

	app "github.com/GizClaw/opencraft/internal/app"
	"github.com/GizClaw/opencraft/internal/config"
	"github.com/GizClaw/opencraft/internal/execd"
	"github.com/GizClaw/opencraft/internal/tui"
)

func main() {
	configPath := flag.String("config", "", "deploy document path (default: embedded, overridable by ~/.opencraft/config/opencraft.yaml)")
	flag.Parse()
	_ = app.LoadDotEnv(".env")
	if dir, err := config.UserDataDir(); err == nil {
		_ = app.LoadDotEnv(filepath.Join(dir, ".env"))
	}

	if *configPath == "" {
		if _, err := config.EnsureUserConfig(); err != nil {
			fmt.Fprintf(os.Stderr, "opencraft: seed config: %v\n", err)
			os.Exit(1)
		}
	}
	// The execd subcommand is self-forked by the main process and
	// must not build a full runtime (it only serves process sessions).
	if flag.Arg(0) == "execd" {
		runExecServer()
		return
	}
	workDir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "opencraft: workdir: %v\n", err)
		os.Exit(1)
	}
	mgr, err := config.Open(config.Options{
		WorkDir:  workDir,
		Explicit: *configPath,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "opencraft: open config: %v\n", err)
		os.Exit(1)
	}
	ctx := context.Background()
	view, err := mgr.Load(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "opencraft: load config: %v\n", err)
		os.Exit(1)
	}
	bridge := tui.NewBridge(256)
	rtc, err := app.NewRuntimeController(ctx, view.Document,
		app.WithConfigBase(mgr.UserDir()),
		app.WithUserPrompter(bridge))
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
		if err := tui.Run(rtc, tui.Options{}, bridge); err != nil {
			fmt.Fprintf(os.Stderr, "opencraft: tui: %v\n", err)
			os.Exit(1)
		}
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
	runner, policy, err := app.SandboxRunner(ctx, *workDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "opencraft execd: %v\n", err)
		os.Exit(1)
	}

	if *listen == "" {
		srv := execd.New(runner, os.Stdin, os.Stdout)
		srv.DefaultEnv = policy
		if err := srv.Serve(ctx); err != nil {
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
			srv := execd.New(runner, conn, conn)
			srv.DefaultEnv = policy
			_ = srv.Serve(ctx)
		}()
	}
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
		for e := errors.Unwrap(err); e != nil; e = errors.Unwrap(e) {
			fmt.Fprintf(os.Stderr, "opencraft:   caused by: %v\n", e)
		}
		os.Exit(1)
	}
	if res.Status != agent.StatusCompleted {
		if res.Err != nil {
			fmt.Fprintf(os.Stderr, "opencraft: turn failed: %v\n", res.Err)
			for err := errors.Unwrap(res.Err); err != nil; err = errors.Unwrap(err) {
				fmt.Fprintf(os.Stderr, "opencraft:   caused by: %v\n", err)
			}
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
