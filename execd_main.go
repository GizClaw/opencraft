package main

// This file hosts the internal execd child mode of the opencraft
// binary: the desktop process self-forks (`opencraft execd ...`) when
// the sandbox needs an isolated process server, so the child serves
// the exec JSON-RPC protocol on stdio or a unix socket.

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/GizClaw/flowcraft/core/telemetry"
	otellog "go.opentelemetry.io/otel/log"

	"github.com/GizClaw/opencraft/internal/capabilities/execd"
	"github.com/GizClaw/opencraft/internal/capabilities/sandbox"
	"github.com/GizClaw/opencraft/internal/foundation/config"
)

// initChildLogging sends this process's warnings to stderr. The child is
// forked before the desktop installs its log pipeline, so without a
// provider of its own every telemetry call inside it would be a no-op.
// Stderr is the one channel the parent can pick up (stdout carries the
// JSON-RPC protocol in stdio mode); fork.go forwards what lands there
// into the application log. It returns nil when the pipeline could not
// be installed, in which case the failure itself is the last diagnostic.
func initChildLogging(ctx context.Context) func(context.Context) error {
	opts := make([]telemetry.LogOption, 0, 2)
	for _, processor := range telemetry.ConsoleProcessors(otellog.SeverityWarn) {
		opts = append(opts, telemetry.WithLogProcessor(processor))
	}
	stop, err := telemetry.InitLog(ctx, opts...)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr,
			"opencraft execd: start log pipeline: %v\n", err)
		return nil
	}
	return stop
}

func runExecServer() {
	fs := flag.NewFlagSet("execd", flag.ExitOnError)
	listen := fs.String("listen", "",
		"unix socket path to listen on (empty: serve on stdio)")
	workDir := fs.String("workdir", "", "working directory (default: current)")
	sandboxPolicy := fs.String("sandbox-policy", "",
		"JSON-encoded sandbox policy from the parent (writable paths + env policy)")
	sandboxPolicyFile := fs.String("sandbox-policy-file", "",
		"path to a 0600 JSON sandbox policy file from the parent; read once and removed")
	parentPid := fs.Int("parent-pid", 0,
		"exit when this parent process dies (0: disabled)")
	if err := fs.Parse(os.Args[2:]); err != nil {
		execdFatal(2, "opencraft execd: %v", err)
	}

	ctx := context.Background()
	if stopLog := initChildLogging(ctx); stopLog != nil {
		defer func() {
			shutdownCtx, cancel := context.WithTimeout(
				context.Background(), 5*time.Second)
			defer cancel()
			if err := stopLog(shutdownCtx); err != nil {
				_, _ = fmt.Fprintf(os.Stderr,
					"opencraft execd: close log pipeline: %v\n", err)
			}
		}()
	}
	if _, err := config.EnsureUserConfig(); err != nil {
		execdFatal(1, "opencraft execd: seed config: %v", err)
	}
	var pol sandbox.SandboxPolicy
	var policyJSON string
	if *sandboxPolicyFile != "" {
		data, err := os.ReadFile(*sandboxPolicyFile)
		if err != nil {
			execdFatal(1, "opencraft execd: sandbox policy file: %v", err)
		}
		policyJSON = string(data)
		// The policy is only needed at startup; remove it so it does
		// not linger on disk (and is never visible through argv).
		telemetry.WarnErr(ctx, "execd main: remove sandbox policy file failed",
			os.Remove(*sandboxPolicyFile))
	} else {
		policyJSON = *sandboxPolicy
	}
	if policyJSON != "" {
		if err := json.Unmarshal([]byte(policyJSON), &pol); err != nil {
			execdFatal(1, "opencraft execd: sandbox policy: %v", err)
		}
	}
	runner, policy, err := sandbox.SandboxRunner(ctx, *workDir, pol)
	if err != nil {
		execdFatal(1, "opencraft execd: %v", err)
	}

	if *listen == "" {
		srv := execd.New(runner, os.Stdin, os.Stdout)
		srv.DefaultEnv = policy
		unconfined, err := sandbox.UnconfinedRunner(*workDir)
		if err != nil {
			execdFatal(1, "opencraft execd: %v", err)
		}
		srv.SetUnconfinedBackend(unconfined)
		if err := srv.Serve(ctx); err != nil {
			execdFatal(1, "opencraft execd: %v", err)
		}
		telemetry.WarnErr(ctx, "execd main: close stdio runner failed",
			runner.Close())
		return
	}
	// Create the socket user-only from the start: chmod after Listen
	// leaves a window where the file is world-visible per the umask.
	restoreUmask := execdSocketUmask()
	listener, err := net.Listen("unix", *listen)
	restoreUmask()
	if err != nil {
		execdFatal(1, "opencraft execd: listen: %v", err)
	}
	defer func() {
		telemetry.WarnErr(context.Background(),
			"execd main: close listener failed", listener.Close())
	}()
	defer func() {
		telemetry.WarnErr(context.Background(),
			"execd main: remove listen socket failed", os.Remove(*listen))
	}()
	telemetry.WarnErr(context.Background(),
		"execd main: secure listen socket failed", os.Chmod(*listen, 0o600))

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
			telemetry.WarnErr(context.Background(),
				"execd main: close listener during shutdown failed",
				listener.Close())
			connMu.Lock()
			for c := range conns {
				telemetry.WarnErr(context.Background(),
					"execd main: close connection during shutdown failed",
					c.Close())
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
				telemetry.WarnErr(ctx,
					"execd main: close connection failed", conn.Close())
			}()
			srv := execd.New(runner, conn, conn)
			srv.DefaultEnv = policy
			unconfined, err := sandbox.UnconfinedRunner(*workDir)
			if err != nil {
				execdFatal(1, "opencraft execd: %v", err)
			}
			srv.SetUnconfinedBackend(unconfined)
			telemetry.WarnErr(ctx, "execd main: serve connection failed",
				srv.Serve(ctx))
		}()
	}
	servesDone := make(chan struct{})
	go func() {
		serveWG.Wait()
		close(servesDone)
	}()
	select {
	case <-servesDone:
	case <-time.After(serveGrace):
	}
	telemetry.WarnErr(ctx, "execd main: close socket runner failed",
		runner.Close())
}

func execdFatal(code int, format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(code)
}
