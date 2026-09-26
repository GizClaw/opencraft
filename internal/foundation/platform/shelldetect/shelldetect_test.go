package shelldetect

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// fakeProbe builds a probe from a PATH-style map plus an optional set
// of existing absolute paths.
func fakeProbe(
	onPath map[string]string, existing ...string,
) probe {
	files := map[string]bool{}
	for _, path := range existing {
		files[path] = true
	}
	return probe{
		lookPath: func(name string) (string, error) {
			if path, ok := onPath[name]; ok {
				return path, nil
			}
			return "", errors.New("executable file not found in $PATH")
		},
		getenv: func(key string) string {
			if key == "SystemRoot" {
				return `C:\Windows`
			}
			return ""
		},
		stat: func(path string) error {
			if files[path] {
				return nil
			}
			return errors.New("no such file or directory")
		},
	}
}

func TestDefault(t *testing.T) {
	for _, goos := range []string{"darwin", "linux"} {
		spec := Default(goos)
		if spec.Program != "/bin/sh" || !reflect.DeepEqual(
			spec.Args, []string{"-c"}) {
			t.Fatalf("%s spec = %+v", goos, spec)
		}
		if spec.SafeRunes != "" {
			t.Fatalf("%s safe runes = %q, want none", goos, spec.SafeRunes)
		}
		if got := spec.CommandLine(); got != "/bin/sh -c" {
			t.Fatalf("%s command line = %q", goos, got)
		}
	}
	win := Default("windows")
	if win.Program != "cmd.exe" ||
		!reflect.DeepEqual(win.Args, []string{"/c"}) ||
		!strings.ContainsRune(win.SafeRunes, '\\') {
		t.Fatalf("windows spec = %+v", win)
	}
	if got := win.CommandLine(); got != "cmd.exe /c" {
		t.Fatalf("windows command line = %q", got)
	}
}

func TestDetectNonWindowsIgnoresProbe(t *testing.T) {
	// The POSIX branch never probes: /bin/sh is the platform contract.
	p := fakeProbe(map[string]string{"pwsh.exe": `C:\pwsh.exe`})
	for _, goos := range []string{"darwin", "linux"} {
		if got := detect(goos, p); !reflect.DeepEqual(got, Default(goos)) {
			t.Fatalf("%s spec = %+v, want the default", goos, got)
		}
	}
}

func TestDetectWindowsPrefersPowerShell7(t *testing.T) {
	p := fakeProbe(map[string]string{
		"pwsh.exe":       `C:\tools\pwsh.exe`,
		"powershell.exe": `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`,
		"cmd.exe":        `C:\Windows\System32\cmd.exe`,
	})
	got := detect("windows", p)
	if got.Program != `C:\tools\pwsh.exe` {
		t.Fatalf("program = %q, want pwsh 7", got.Program)
	}
	if !reflect.DeepEqual(got.Args, []string{"-NoProfile", "-Command"}) {
		t.Fatalf("args = %v", got.Args)
	}
	if !strings.ContainsRune(got.SafeRunes, '\\') {
		t.Fatalf("safe runes = %q", got.SafeRunes)
	}
}

func TestDetectWindowsFallsBackToWindowsPowerShell(t *testing.T) {
	p := fakeProbe(map[string]string{
		"powershell.exe": `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`,
	}, `C:\Program Files\PowerShell\7\pwsh.exe`)
	got := detect("windows", p)
	// pwsh is on disk but not on PATH: the well-known location wins over
	// falling through to 5.1.
	if got.Program != pwshFallback {
		t.Fatalf("program = %q, want the pwsh fallback path", got.Program)
	}

	// Without the pwsh fallback on disk, 5.1 is next.
	got = detect("windows", fakeProbe(map[string]string{
		"powershell.exe": `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`,
	}))
	if got.Program != `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe` {
		t.Fatalf("program = %q, want Windows PowerShell", got.Program)
	}
	if !reflect.DeepEqual(got.Args, []string{"-NoProfile", "-Command"}) {
		t.Fatalf("args = %v", got.Args)
	}
}

func TestDetectWindowsFallsBackToCmd(t *testing.T) {
	got := detect("windows", fakeProbe(map[string]string{
		"cmd.exe": `C:\Windows\System32\cmd.exe`,
	}))
	if got.Program != `C:\Windows\System32\cmd.exe` {
		t.Fatalf("program = %q, want cmd.exe", got.Program)
	}
	if !reflect.DeepEqual(got.Args, []string{"/c"}) {
		t.Fatalf("args = %v", got.Args)
	}

	// Nothing found at all: the literal default keeps the tool usable
	// (CreateProcess resolves it through the system directory).
	got = detect("windows", fakeProbe(nil))
	if !reflect.DeepEqual(got, Default("windows")) {
		t.Fatalf("spec = %+v, want the default", got)
	}
}

func TestDetectWindowsSkipsStoreAliases(t *testing.T) {
	// The Store / AppExecutionAlias stubs exist on many machines but a
	// sandbox token cannot follow the reparse point, so they must not
	// win over the real install.
	p := fakeProbe(map[string]string{
		"pwsh.exe": `C:\Users\user\AppData\Local\Microsoft\WindowsApps\pwsh.exe`,
	}, `C:\Program Files\PowerShell\7\pwsh.exe`)
	if got := detect("windows", p); got.Program != pwshFallback {
		t.Fatalf("program = %q, want the real pwsh install", got.Program)
	}

	// A Store-only machine falls through to cmd once the alias is
	// rejected, rather than selecting a shell that cannot start.
	got := detect("windows", fakeProbe(map[string]string{
		"pwsh.exe": `C:\Program Files\WindowsApps\Microsoft.PowerShell_7_x64__8wekyb3d8bbwe\pwsh.exe`,
		"cmd.exe":  `C:\Windows\System32\cmd.exe`,
	}))
	if got.Program != `C:\Windows\System32\cmd.exe` {
		t.Fatalf("program = %q, want cmd.exe", got.Program)
	}
}

func TestDetectAgainstHost(t *testing.T) {
	// Detect() itself must always answer with something runnable-shaped.
	for _, goos := range []string{"darwin", "linux", "windows"} {
		if spec := Detect(goos); spec.Program == "" {
			t.Fatalf("%s: empty program", goos)
		}
	}
}
