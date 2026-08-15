// Command opencraft is the opencraft coding agent entrypoint. It loads a
// deploy document (opencraft.yaml) and assembles the runtime through
// flowcraft's config-driven deploy machinery.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/message"
	sessions "github.com/GizClaw/flowcraft/core/runtime/session"

	app "github.com/GizClaw/opencraft/internal/app"
	"github.com/GizClaw/opencraft/internal/config"
	"github.com/GizClaw/opencraft/internal/execd"
	"github.com/GizClaw/opencraft/internal/interact"
	ocsessions "github.com/GizClaw/opencraft/internal/sessions"
	"github.com/GizClaw/opencraft/internal/tui"
)

func main() {
	configPath := flag.String("config", "", "deploy document path (default: embedded, overridable by ~/.opencraft/config/opencraft.yaml)")
	flag.Parse()

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
		app.WithUsageObserver(bridge.Usage))
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
		broker := interact.New(rtc.Runtime(), bridge)
		if err := broker.Attach(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "opencraft: attach broker: %v\n", err)
			os.Exit(1)
		}
		defer broker.Close()
		store, err := ocsessions.New(
			filepath.Join(workDir, ".opencraft", "sessions"), 40)
		if err != nil {
			fmt.Fprintf(os.Stderr, "opencraft: session store: %v\n", err)
			os.Exit(1)
		}
		if err := tui.Run(rtc, tui.Options{
			Model:   config.DefaultModel(mgr.UserDir()),
			WorkDir: workDir,
			// Every TUI launch starts a fresh conversation; /resume
			// switches to an existing session id.
			ContextID: ocsessions.NewID(),
			Sessions:  store,
		}, bridge, broker); err != nil {
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
	sandboxPolicy := fs.String("sandbox-policy", "",
		"JSON-encoded sandbox policy from the parent (writable paths + env policy)")
	parentPid := fs.Int("parent-pid", 0,
		"exit when this parent process dies (0: disabled)")
	if err := fs.Parse(flag.Args()[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "opencraft execd: %v\n", err)
		os.Exit(2)
	}

	ctx := context.Background()
	if _, err := config.EnsureUserConfig(); err != nil {
		fmt.Fprintf(os.Stderr, "opencraft execd: seed config: %v\n", err)
		os.Exit(1)
	}
	var pol app.SandboxPolicy
	if *sandboxPolicy != "" {
		if err := json.Unmarshal([]byte(*sandboxPolicy), &pol); err != nil {
			fmt.Fprintf(os.Stderr, "opencraft execd: sandbox policy: %v\n", err)
			os.Exit(1)
		}
	}
	runner, policy, err := app.SandboxRunner(ctx, *workDir, pol)
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
		_ = runner.Close()
		return
	}
	listener, err := net.Listen("unix", *listen)
	if err != nil {
		fmt.Fprintf(os.Stderr, "opencraft execd: listen: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = listener.Close() }()
	defer func() { _ = os.Remove(*listen) }()
	_ = os.Chmod(*listen, 0o600)

	// The accept loop alone would outlive the parent: when the parent
	// exits, established connections EOF and each Serve cleans up, but
	// Accept keeps blocking forever, leaving an orphan that holds the
	// socket. Watch the parent and also honor SIGTERM/SIGINT so every
	// exit path closes the listener and terminates in-flight sessions.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const serveGrace = 5 * time.Second
	var serveWG sync.WaitGroup
	var connMu sync.Mutex
	conns := make(map[net.Conn]struct{})

	shutdown := make(chan struct{})
	var shutdownOnce sync.Once
	stopAccepting := func() {
		shutdownOnce.Do(func() {
			close(shutdown)
			_ = listener.Close()
			connMu.Lock()
			for c := range conns {
				_ = c.Close()
			}
			connMu.Unlock()
		})
	}

	if *parentPid > 0 {
		go watchParent(*parentPid, stopAccepting)
	}
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(sigCh)
	go func() {
		select {
		case <-sigCh:
			stopAccepting()
		case <-shutdown:
		}
	}()

	for {
		conn, err := listener.Accept()
		if err != nil {
			break
		}
		connMu.Lock()
		conns[conn] = struct{}{}
		connMu.Unlock()
		serveWG.Add(1)
		go func() {
			defer serveWG.Done()
			defer func() {
				connMu.Lock()
				delete(conns, conn)
				connMu.Unlock()
				_ = conn.Close()
			}()
			srv := execd.New(runner, conn, conn)
			srv.DefaultEnv = policy
			_ = srv.Serve(ctx)
		}()
	}
	// Listener closed: stop accepting and give in-flight serves a
	// bounded window to terminate their sessions before the process
	// exits. On parent death the connections have already EOF'd, so
	// this normally completes immediately.
	servesDone := make(chan struct{})
	go func() {
		serveWG.Wait()
		close(servesDone)
	}()
	select {
	case <-servesDone:
	case <-time.After(serveGrace):
	}
	_ = runner.Close()
}

// watchParent invokes onDeath once ppid stops being the caller's
// parent. When a process dies, its children are reparented (to launchd
// or init), so a changed Getppid is a reliable death signal without
// relying on PID-reuse-prone kill probes. Polling keeps this portable
// across macOS and Linux; the window between parent death and cleanup
// is at most one tick.
func watchParent(ppid int, onDeath func()) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for range ticker.C {
		if os.Getppid() != ppid {
			onDeath()
			return
		}
	}
}

func run(rtc *app.RuntimeController, text string) {
	if text == "" {
		fmt.Fprintln(os.Stderr, "opencraft: run requires a message")
		os.Exit(2)
	}
	ctx := context.Background()
	rt := rtc.Runtime()
	broker := interact.New(rt, interact.Auto{})
	if err := broker.Attach(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "opencraft: attach broker: %v\n", err)
		os.Exit(1)
	}
	defer broker.Close()

	key := sessions.Key{AgentID: "assistant", ContextID: "cli"}
	lease, err := rt.Sessions().Open(ctx, key)
	if err != nil {
		fmt.Fprintf(os.Stderr, "opencraft: open session: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = lease.Close() }()

	turn, err := lease.Session().Start(ctx, agent.Request{
		ContextID: key.ContextID,
		Message:   message.NewTextMessage(message.RoleUser, text),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "opencraft: start turn: %v\n", err)
		os.Exit(1)
	}
	broker.BindTurn(turn.RunID(), turn)
	defer broker.UnbindTurn(turn.RunID())
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
