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
	"strconv"
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
	"github.com/GizClaw/opencraft/internal/sandbox"
	ocsessions "github.com/GizClaw/opencraft/internal/sessions"
	"github.com/GizClaw/opencraft/internal/telemetry"
	"github.com/GizClaw/opencraft/internal/tui"
)

// telemetryShutdown flushes and tears down the OTel pipelines. It is
// also drained explicitly by fatal before os.Exit, which skips defers.
var telemetryShutdown func(context.Context) error

func main() {
	configPath := flag.String("config", "", "deploy document path (default: embedded, overridable by ~/.opencraft/config/opencraft.yaml)")
	otelEndpoint := flag.String("otel-endpoint", os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"),
		"OTLP/HTTP collector endpoint host[:port] (scheme optional); env OTEL_EXPORTER_OTLP_ENDPOINT")
	otelInsecure := flag.Bool("otel-insecure", envBool("OTEL_EXPORTER_OTLP_INSECURE", false),
		"disable TLS for OTLP (auto-enabled for http:// and loopback endpoints); env OTEL_EXPORTER_OTLP_INSECURE")
	logFile := flag.String("log-file", os.Getenv("OPENCRAFT_LOG_FILE"),
		"rotating plain-text log file (default: ~/.opencraft/logs/<mode>.log); env OPENCRAFT_LOG_FILE")
	flag.Parse()

	// Logs go to ~/.opencraft/logs by default (execd gets its own
	// file so the self-forked child does not share a lumberjack file
	// with the parent). Nothing is written to the console: the TUI
	// owns stdout and execd's stdio mode uses stdout as its protocol
	// channel.
	logPath := *logFile
	if logPath == "" {
		dataDir, err := config.UserDataDir()
		if err != nil {
			fatal(1, "opencraft: log dir: %v", err)
		}
		logName := "opencraft.log"
		if flag.Arg(0) == "execd" {
			logName = "execd.log"
		}
		logPath = filepath.Join(dataDir, "logs", logName)
	}

	// Initialize telemetry before any work so startup failures are
	// captured too.
	shutdown, err := telemetry.Init(context.Background(), telemetry.Options{
		OTLPEndpoint: *otelEndpoint,
		OTLPInsecure: *otelInsecure,
		LogFile:      logPath,
	})
	if err != nil {
		fatal(1, "opencraft: init telemetry: %v", err)
	}
	telemetryShutdown = shutdown
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = shutdown(ctx)
	}()

	if *configPath == "" {
		if _, err := config.EnsureUserConfig(); err != nil {
			fatal(1, "opencraft: seed config: %v", err)
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
		fatal(1, "opencraft: workdir: %v", err)
	}
	mgr, err := config.Open(config.Options{
		WorkDir:  workDir,
		Explicit: *configPath,
	})
	if err != nil {
		fatal(1, "opencraft: open config: %v", err)
	}
	ctx := context.Background()
	view, err := mgr.Load(ctx)
	if err != nil {
		fatal(1, "opencraft: load config: %v", err)
	}
	bridge := tui.NewBridge(256)
	rtc, err := app.NewRuntimeController(ctx, view.Document,
		app.WithConfigBase(mgr.UserDir()),
		app.WithUsageObserver(bridge.Usage))
	if err != nil {
		fatal(1, "opencraft: assemble runtime: %v", err)
	}
	defer func() {
		if err := rtc.Close(); err != nil {
			telemetry.Error(context.Background(), fmt.Sprintf("opencraft: close runtime: %v", err))
		}
	}()

	switch flag.Arg(0) {
	case "run":
		run(rtc, flag.Arg(1))
	case "":
		broker := interact.New(rtc.Runtime(), bridge)
		if err := broker.Attach(ctx); err != nil {
			fatal(1, "opencraft: attach broker: %v", err)
		}
		defer broker.Close()
		store, err := ocsessions.New(
			filepath.Join(workDir, ".opencraft", "sessions"), 40)
		if err != nil {
			fatal(1, "opencraft: session store: %v", err)
		}
		if err := tui.Run(rtc, tui.Options{
			Model:   config.DefaultModel(mgr.UserDir()),
			WorkDir: workDir,
			// Every TUI launch starts a fresh conversation; /resume
			// switches to an existing session id.
			ContextID: ocsessions.NewID(),
			Sessions:  store,
		}, bridge, broker); err != nil {
			fatal(1, "opencraft: tui: %v", err)
		}
	default:
		fatal(2, "opencraft: unknown command %q (run)", flag.Arg(0))
	}
}

// envBool parses a boolean environment variable, falling back to def
// when unset or unparsable (accepts 1/t/true/yes and their false forms).
func envBool(key string, def bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

// fatal logs msg through the telemetry pipeline, drains it so batched
// records reach their sinks, and exits with code. Telemetry may not be
// initialized yet during early startup (log-dir resolution or the
// telemetry pipeline itself failed); in that window the message falls
// back to stderr, the only sink that exists.
func fatal(code int, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	// Safe no-op before Init: the global OTel logger provider defaults
	// to a no-op until the pipelines are installed.
	telemetry.Error(context.Background(), msg)
	if telemetryShutdown == nil {
		fmt.Fprintf(os.Stderr, "%s\n", msg)
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = telemetryShutdown(ctx)
	}
	os.Exit(code)
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
		fatal(2, "opencraft execd: %v", err)
	}

	ctx := context.Background()
	if _, err := config.EnsureUserConfig(); err != nil {
		fatal(1, "opencraft execd: seed config: %v", err)
	}
	var pol sandbox.SandboxPolicy
	if *sandboxPolicy != "" {
		if err := json.Unmarshal([]byte(*sandboxPolicy), &pol); err != nil {
			fatal(1, "opencraft execd: sandbox policy: %v", err)
		}
	}
	runner, policy, err := sandbox.SandboxRunner(ctx, *workDir, pol)
	if err != nil {
		fatal(1, "opencraft execd: %v", err)
	}

	if *listen == "" {
		srv := execd.New(runner, os.Stdin, os.Stdout)
		srv.DefaultEnv = policy
		srv.SetUnconfinedBackend(sandbox.UnconfinedRunner(*workDir))
		if err := srv.Serve(ctx); err != nil {
			fatal(1, "opencraft execd: %v", err)
		}
		_ = runner.Close()
		return
	}
	listener, err := net.Listen("unix", *listen)
	if err != nil {
		fatal(1, "opencraft execd: listen: %v", err)
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
			srv.SetUnconfinedBackend(sandbox.UnconfinedRunner(*workDir))
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
		fatal(2, "opencraft: run requires a message")
	}
	ctx := context.Background()
	rt := rtc.Runtime()
	broker := interact.New(rt, interact.Auto{})
	if err := broker.Attach(ctx); err != nil {
		fatal(1, "opencraft: attach broker: %v", err)
	}
	defer broker.Close()

	key := sessions.Key{AgentID: "assistant", ContextID: "cli"}
	lease, err := rt.Sessions().Open(ctx, key)
	if err != nil {
		fatal(1, "opencraft: open session: %v", err)
	}
	defer func() { _ = lease.Close() }()

	turn, err := lease.Session().Start(ctx, agent.Request{
		ContextID: key.ContextID,
		Message:   message.NewTextMessage(message.RoleUser, text),
	})
	if err != nil {
		fatal(1, "opencraft: start turn: %v", err)
	}
	broker.BindTurn(turn.RunID(), turn)
	defer broker.UnbindTurn(turn.RunID())
	res, err := turn.Wait(ctx)
	if err != nil {
		telemetry.Error(ctx, fmt.Sprintf("opencraft: turn: %v", err))
		for e := errors.Unwrap(err); e != nil; e = errors.Unwrap(e) {
			telemetry.Error(ctx, fmt.Sprintf("opencraft:   caused by: %v", e))
		}
		fatal(1, "opencraft: turn failed")
	}
	if res.Status != agent.StatusCompleted {
		if res.Err != nil {
			telemetry.Error(ctx, fmt.Sprintf("opencraft: turn failed: %v", res.Err))
			for err := errors.Unwrap(res.Err); err != nil; err = errors.Unwrap(err) {
				telemetry.Error(ctx, fmt.Sprintf("opencraft:   caused by: %v", err))
			}
		} else {
			telemetry.Error(ctx, fmt.Sprintf("opencraft: turn status=%s", res.Status))
		}
		fatal(1, "opencraft: turn did not complete")
	}
	for _, msg := range res.Messages {
		if msg.Role == message.RoleAssistant {
			// CLI program output (the answer), not a log: it must reach
			// stdout for pipes/scripts, so it stays out of telemetry.
			fmt.Println(msg.Content.Text())
		}
	}
}
