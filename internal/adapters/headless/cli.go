package headless

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/GizClaw/opencraft/internal/capabilities/execd"
	"github.com/GizClaw/opencraft/internal/foundation/config"
)

// Main implements `opencraft run`. Exit codes: 0 completed, 1 failed,
// 2 usage/runtime setup error.
func Main(args []string) int {
	// The roots come from the same resolver the desktop entry point
	// uses, so `opencraft run --data-dir X` and `./bin/opencraft
	// --data-dir X` mean the same thing (--profile included). The
	// parser below re-declares the root flags so they stay in the usage
	// text; the resolver already applied them.
	launch, err := config.ResolveLaunch(args, os.Getenv)
	if err != nil {
		fmt.Fprintf(os.Stderr, "opencraft run: %v\n", err)
		return 2
	}
	// `opencraft run` gets the same exec supervisor pool as the desktop
	// shell, with the shipped defaults (there is no settings UI here).
	pool := execd.NewPool(execd.DefaultPoolSettings())
	execd.SetDefaultPool(pool)
	defer func() {
		execd.SetDefaultPool(nil)
		pool.Close()
	}()
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	workdir := fs.String("workdir", launch.WorkDir, "workspace root (default: current directory)")
	configDir := fs.String("config-dir", launch.ConfigDir, "user config directory (default: ~/.opencraft/config)")
	fs.StringVar(configDir, "config", launch.ConfigDir, "alias of --config-dir")
	dataDir := fs.String("data-dir", launch.DataDir, "state root (default: the config directory's parent)")
	appHome := fs.String("app-home", launch.AppHome, "shared content and credential root (default: the state root)")
	fs.String("profile", launch.Profile, "state-root profile (resolved before this parser runs)")
	prompt := fs.String("prompt", "", "user prompt to run")
	promptFile := fs.String("prompt-file", "", "read the prompt from a file")
	jsonOut := fs.Bool("json", false, "emit JSONL rollout events on stdout")
	repair := fs.Bool("repair-config", false,
		"remove retired ${env:OPEN_CRAFT_*} references from the user config layer and exit")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if *repair {
		return repairConfig(*configDir)
	}

	text := strings.TrimSpace(*prompt)
	if text == "" && *promptFile != "" {
		data, err := os.ReadFile(*promptFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "opencraft run: read prompt file: %v\n", err)
			return 2
		}
		text = strings.TrimSpace(string(data))
	}
	if text == "" {
		fmt.Fprintln(os.Stderr, "opencraft run: --prompt or --prompt-file is required")
		return 2
	}

	var out io.Writer
	if *jsonOut {
		out = os.Stdout
	}
	result, err := Run(context.Background(), Options{
		WorkDir:   *workdir,
		ConfigDir: *configDir,
		DataDir:   *dataDir,
		AppHome:   *appHome,
		Prompt:    text,
		Out:       out,
		// JSONL consumers parse stdout line by line, so the state-root
		// note stays out of their stream.
		Quiet: *jsonOut,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "opencraft run: %v\n", err)
		return 1
	}
	if !*jsonOut {
		fmt.Printf("status: %s\n", result.Status)
		fmt.Printf("conversation: %s\n", result.ConversationID)
		fmt.Printf("run: %s\n", result.RunID)
		if result.Error != "" {
			fmt.Printf("error: %s\n", result.Error)
		}
	}
	return result.ExitCode
}

// repairConfig implements --repair-config: the headless counterpart of
// the desktop Diagnostics repair. A user layer that still carries
// retired ${env:OPEN_CRAFT_*} references fails every load (see
// config.Manager.Load), and a headless-only deployment has no settings
// page to offer the button on, so the fix has to be reachable from the
// CLI. The pre-repair document is kept as a .bak next to the layer.
func repairConfig(configDir string) int {
	res, err := config.RepairRetiredRefs(configDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "opencraft run: repair config: %v\n", err)
		return 1
	}
	if len(res.Removed) == 0 {
		fmt.Printf("config: %s has no retired references\n", res.File)
		return 0
	}
	fmt.Printf(
		"config: removed %d retired reference(s) from %s (pre-repair copy: %s)\n",
		len(res.Removed), res.File, res.Backup)
	for _, path := range res.Removed {
		fmt.Printf("  removed %s\n", path)
	}
	return 0
}
