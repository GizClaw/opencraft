package main

// This file hosts the internal execd child mode of the opencraft
// binary: the host self-forks (`opencraft execd ...`) and hands the
// child a private IPC channel (a socketpair fd on Unix, a named pipe on
// Windows). The child starts unbound and receives its workspace through
// the Bind request.

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/GizClaw/flowcraft/core/telemetry"
	otellog "go.opentelemetry.io/otel/log"

	"github.com/GizClaw/opencraft/internal/capabilities/execd"
	"github.com/GizClaw/opencraft/internal/capabilities/sandbox"
)

// initChildLogging sends this process's warnings to stderr. The child
// is forked before the desktop installs its log pipeline, so without a
// provider of its own every telemetry call inside it would be a no-op.
// Stderr is the one channel the parent can pick up (stdout and the IPC
// channel carry nothing but the protocol).
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
	execdFD := fs.Int("execd-fd", 0,
		"inherited socketpair descriptor (unix)")
	execdPipe := fs.String("execd-pipe", "",
		"named pipe path (windows)")
	_ = fs.String("execd-nonce", "",
		"per-launch identity nonce recorded in the parent's orphan journal")
	if err := fs.Parse(os.Args[2:]); err != nil {
		execdFatal(2, "opencraft execd: %v", err)
	}

	baseCtx := context.Background()
	if stopLog := initChildLogging(baseCtx); stopLog != nil {
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
	// The child resolves no user directories of its own: every path it
	// needs (the bound workspace, the sandbox policy's writable set and
	// its cache root) arrives with the Bind request, so a dev-profile
	// parent never has the child seed or touch the installed app's root.

	conn, err := execd.ChildChannel(*execdFD, *execdPipe)
	if err != nil {
		execdFatal(1, "opencraft execd: channel: %v", err)
	}

	factory := func(
		ctx context.Context,
		workdir string,
		policy *execd.SandboxPolicy,
	) (execd.RunnerSet, error) {
		sandboxPolicy := sandbox.SandboxPolicy{
			WritablePaths: policy.GetWritablePaths(),
		}
		if policy.GetEnvAllowSet() || len(policy.GetEnvInject()) > 0 {
			sandboxPolicy.EnvPolicy = &sandbox.EnvPolicyConfig{
				Allow:  policy.GetEnvAllow(),
				Inject: policy.GetEnvInject(),
			}
			if policy.GetEnvAllowSet() && sandboxPolicy.EnvPolicy.Allow == nil {
				sandboxPolicy.EnvPolicy.Allow = []string{}
			}
		}
		// The cache root travels with the binding: the parent resolved
		// it from its own state root, and the child must not guess one.
		confined, env, err := sandbox.SandboxRunnerForCache(
			ctx, workdir, policy.GetCacheDir(), sandboxPolicy)
		if err != nil {
			return execd.RunnerSet{}, err
		}
		unconfined, err := sandbox.UnconfinedRunner(workdir)
		if err != nil {
			telemetry.WarnErr(ctx,
				"opencraft execd: close confined runner after unconfined failure",
				confined.Close())
			return execd.RunnerSet{}, err
		}
		return execd.RunnerSet{
			Confined:   confined,
			Unconfined: unconfined,
			DefaultEnv: env,
		}, nil
	}

	ctx, cancel := context.WithCancel(baseCtx)
	defer cancel()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(sigCh)
	go func() {
		select {
		case <-sigCh:
			cancel()
		case <-ctx.Done():
		}
	}()

	server := execd.New(factory, conn, conn)
	if err := server.Serve(ctx); err != nil {
		execdFatal(1, "opencraft execd: %v", err)
	}
}

func execdFatal(code int, format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(code)
}
