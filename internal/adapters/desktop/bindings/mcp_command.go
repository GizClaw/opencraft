package bindings

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/GizClaw/opencraft/internal/foundation/config"
)

// mcpCommandProblem reports why this process cannot start a stdio MCP
// server's command, or nil when the spawn is expected to succeed.
//
// The settings page consults it before the readiness probe: a command
// that cannot be spawned keeps failing inside the source's background
// retry loop, which retries forever instead of giving up, so the probe
// alone reports "connecting" for a server that will never come up. The
// check is recomputed on every poll, so installing the binary or fixing
// the entry clears the error without a reload.
//
// The rules mirror os/exec (checked against Go 1.27), including the
// asymmetry between the two PATHs involved:
//
//   - a bare command name is resolved with LookPath against *this*
//     process's PATH before any child exists, so env.PATH cannot
//     influence it;
//   - cmd.Env keeps the last occurrence of a key, so a configured
//     env.PATH replaces the inherited PATH for the child;
//   - that child PATH is what a `#!/usr/bin/env <tool>` shebang
//     resolves through, so a script whose interpreter only sits on the
//     user's shell PATH fails even when command is absolute.
//
// This matters because a GUI launch inherits launchd's minimal PATH
// (/usr/bin:/bin:/usr/sbin:/sbin), not the PATH a terminal session
// builds from the user's shell profile.
func mcpCommandProblem(srv config.MCPServer) error {
	if srv.Transport != "stdio" {
		return nil
	}
	command := strings.TrimSpace(srv.Command)
	if command == "" {
		return fmt.Errorf("MCP server %q: command is required for stdio", srv.Name)
	}
	resolved, err := resolveMCPCommand(command)
	if err != nil {
		return fmt.Errorf("MCP server %q: cannot start %q: %w", srv.Name, command, err)
	}
	if err := checkMCPShebang(resolved, mcpServerPATH(srv)); err != nil {
		return fmt.Errorf("MCP server %q: cannot start %q: %w", srv.Name, command, err)
	}
	return nil
}

// mcpServerPATH is the PATH the spawned server runs with, and so the
// PATH its shebang interpreter is looked up in.
func mcpServerPATH(srv config.MCPServer) string {
	if path := srv.Env["PATH"]; path != "" {
		return path
	}
	return os.Getenv("PATH")
}

// resolveMCPCommand mirrors how os/exec turns a command name into an
// executable path.
func resolveMCPCommand(command string) (string, error) {
	if !isBareCommandName(command) {
		if err := executableFile(command); err != nil {
			return "", err
		}
		return command, nil
	}
	resolved, err := exec.LookPath(command)
	if err != nil {
		return "", fmt.Errorf(
			"not found in this app's PATH (%s); set an absolute command path — "+
				"a Finder launch inherits launchd's PATH, not your shell PATH, "+
				"and env.PATH cannot change bare-name resolution",
			os.Getenv("PATH"))
	}
	return resolved, nil
}

// isBareCommandName reports whether os/exec resolves the command with
// LookPath instead of using it as a path. Windows accepts either path
// separator; POSIX has just one.
func isBareCommandName(command string) bool {
	if strings.ContainsRune(command, os.PathSeparator) {
		return false
	}
	return runtime.GOOS != "windows" || !strings.Contains(command, "/")
}

// executableFile reports why path cannot be executed, or nil.
func executableFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return errors.New("no such file")
		}
		return err
	}
	if info.IsDir() {
		return errors.New("is a directory, not an executable")
	}
	// Windows has no execute permission bit, and LookPath already
	// applied PATHEXT to bare names.
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		return errors.New("is not executable")
	}
	return nil
}

// checkMCPShebang reports why a script cannot run under the PATH the
// server is spawned with, or nil for binaries, for absolute
// interpreters that exist, and on Windows, which never starts scripts
// through a shebang.
func checkMCPShebang(path, serverPATH string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	interpreter, argument, ok := readShebang(path)
	if !ok {
		return nil
	}
	if filepath.Base(interpreter) != "env" {
		if filepath.IsAbs(interpreter) {
			if err := executableFile(interpreter); err != nil {
				return fmt.Errorf("its shebang interpreter %q: %w", interpreter, err)
			}
		}
		return nil
	}
	if argument == "" {
		return nil
	}
	if !hasExecutableIn(serverPATH, argument) {
		return fmt.Errorf(
			"its shebang needs %q on the PATH the server runs with (%s); "+
				"add the interpreter's directory to this server's env.PATH",
			argument, serverPATH)
	}
	return nil
}

// readShebang reads the interpreter line of a script. It reports
// ok=false for anything that is not a "#!" script, leaving binaries and
// unreadable files to the spawn itself.
func readShebang(path string) (interpreter, argument string, ok bool) {
	file, err := os.Open(path)
	if err != nil {
		return "", "", false
	}
	head := make([]byte, 256)
	n, readErr := io.ReadFull(file, head)
	// The head is all this needs, so the handle is closed here rather
	// than deferred; a failed close is as inconclusive as a failed
	// read, and both leave the verdict to the spawn itself.
	if closeErr := file.Close(); closeErr != nil || (readErr != nil &&
		!errors.Is(readErr, io.ErrUnexpectedEOF) && !errors.Is(readErr, io.EOF)) {
		return "", "", false
	}
	head = head[:n]
	if !bytes.HasPrefix(head, []byte("#!")) {
		return "", "", false
	}
	line := head[2:]
	if end := bytes.IndexByte(line, '\n'); end >= 0 {
		line = line[:end]
	}
	fields := strings.Fields(string(line))
	if len(fields) == 0 {
		return "", "", false
	}
	// /usr/bin/env takes flags (-S, -i, --) and VAR=VALUE assignments
	// ahead of the program name.
	for _, field := range fields[1:] {
		if strings.HasPrefix(field, "-") || strings.ContainsRune(field, '=') {
			continue
		}
		argument = field
		break
	}
	return fields[0], argument, true
}

// hasExecutableIn resolves name against pathEnv the way the child's
// /usr/bin/env does: first hit wins, an empty entry means the working
// directory.
func hasExecutableIn(pathEnv, name string) bool {
	for _, dir := range filepath.SplitList(pathEnv) {
		if dir == "" {
			dir = "."
		}
		if executableFile(filepath.Join(dir, name)) == nil {
			return true
		}
	}
	return false
}
