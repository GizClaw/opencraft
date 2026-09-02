package desktop

import (
	"errors"
	"net/url"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// revealInFileManager highlights path in the platform file manager.
func revealInFileManager(path string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", "-R", path).Run()
	case "windows":
		// explorer returns immediately; detach it so the launcher does
		// not linger as a zombie.
		cmd := exec.Command("explorer", "/select,", path)
		if err := cmd.Start(); err != nil {
			return err
		}
		_ = cmd.Process.Release()
		return nil
	default:
		// Use the freedesktop FileManager1 D-Bus API when available so
		// the file is selected rather than merely opening the folder.
		fileURL := (&url.URL{Scheme: "file", Path: path}).String()
		cmd := exec.Command(
			"dbus-send",
			"--session",
			"--print-reply",
			"--dest=org.freedesktop.FileManager1",
			"/org/freedesktop/FileManager1",
			"org.freedesktop.FileManager1.ShowItems",
			"array:string:"+fileURL,
			"string:",
		)
		if err := cmd.Run(); err == nil {
			return nil
		}
		cmd = exec.Command("xdg-open", filepath.Dir(path))
		if err := cmd.Start(); err != nil {
			return err
		}
		_ = cmd.Process.Release()
		return nil
	}
}

// openArtifactWith starts the platform "Open With" flow. Linux has no
// portable system chooser, so it opens with the default application.
func openArtifactWith(path, prompt string) error {
	switch runtime.GOOS {
	case "darwin":
		return openArtifactWithMacOS(path, prompt)
	case "windows":
		cmd := exec.Command("rundll32", "shell32.dll,OpenAs_RunDLL", path)
		if err := cmd.Start(); err != nil {
			return err
		}
		_ = cmd.Process.Release()
		return nil
	default:
		cmd := exec.Command("xdg-open", path)
		if err := cmd.Start(); err != nil {
			return err
		}
		_ = cmd.Process.Release()
		return nil
	}
}

func openArtifactWithMacOS(path, prompt string) error {
	prompt = strings.ReplaceAll(prompt, `"`, `\"`)
	script := `POSIX path of (choose application with prompt "` + prompt + `")`
	out, err := exec.Command("osascript", "-e", script).Output()
	if err != nil {
		return err
	}
	app := strings.TrimSpace(string(out))
	if app == "" {
		return errors.New("no application selected")
	}
	cmd := exec.Command("open", "-a", app, path)
	if err := cmd.Start(); err != nil {
		return err
	}
	_ = cmd.Process.Release()
	return nil
}
