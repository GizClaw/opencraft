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

	"github.com/GizClaw/opencraft/internal/config"
	"github.com/GizClaw/opencraft/internal/execd"
	"github.com/GizClaw/opencraft/internal/sandbox"
)

func runExecServer() {
	fs := flag.NewFlagSet("execd", flag.ExitOnError)
	listen := fs.String("listen", "",
		"unix socket path to listen on (empty: serve on stdio)")
	workDir := fs.String("workdir", "", "working directory (default: current)")
	sandboxPolicy := fs.String("sandbox-policy", "",
		"JSON-encoded sandbox policy from the parent (writable paths + env policy)")
	parentPid := fs.Int("parent-pid", 0,
		"exit when this parent process dies (0: disabled)")
	if err := fs.Parse(os.Args[2:]); err != nil {
		execdFatal(2, "opencraft execd: %v", err)
	}

	ctx := context.Background()
	if _, err := config.EnsureUserConfig(); err != nil {
		execdFatal(1, "opencraft execd: seed config: %v", err)
	}
	var pol sandbox.SandboxPolicy
	if *sandboxPolicy != "" {
		if err := json.Unmarshal([]byte(*sandboxPolicy), &pol); err != nil {
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
		srv.SetUnconfinedBackend(sandbox.UnconfinedRunner(*workDir))
		if err := srv.Serve(ctx); err != nil {
			execdFatal(1, "opencraft execd: %v", err)
		}
		_ = runner.Close()
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
	defer func() { _ = listener.Close() }()
	defer func() { _ = os.Remove(*listen) }()
	_ = os.Chmod(*listen, 0o600)

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
// relying on PID-reuse-prone kill probes.
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

func execdFatal(code int, format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(code)
}
