package headless

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

// Main implements `opencraft run`. Exit codes: 0 completed, 1 failed,
// 2 usage/runtime setup error.
func Main(args []string) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	workdir := fs.String("workdir", "", "workspace root (default: current directory)")
	configDir := fs.String("config", "", "user config directory (default: ~/.opencraft/config)")
	prompt := fs.String("prompt", "", "user prompt to run")
	promptFile := fs.String("prompt-file", "", "read the prompt from a file")
	jsonOut := fs.Bool("json", false, "emit JSONL rollout events on stdout")
	if err := fs.Parse(args); err != nil {
		return 2
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
		Prompt:    text,
		Out:       out,
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
