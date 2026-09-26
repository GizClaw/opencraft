package bindings

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
)

// TestOpenArgs pins the hand-off table for every platform on every
// host: the branches are data, so darwin/linux/windows are all
// assertable on one machine.
func TestOpenArgs(t *testing.T) {
	cases := []struct {
		name        string
		goos        string
		mode        openMode
		target      string
		wantProgram string
		wantArgs    []string
	}{
		{
			name: "darwin opens a url", goos: "darwin", mode: openDefault,
			target:      "https://example.com/x",
			wantProgram: "open", wantArgs: []string{"https://example.com/x"},
		},
		{
			name: "darwin opens a file", goos: "darwin", mode: openDefault,
			target:      "/tmp/a.txt",
			wantProgram: "open", wantArgs: []string{"/tmp/a.txt"},
		},
		{
			name: "linux opens a file", goos: "linux", mode: openDefault,
			target:      "/tmp/a.txt",
			wantProgram: "xdg-open", wantArgs: []string{"/tmp/a.txt"},
		},
		{
			name: "windows opens a url", goos: "windows", mode: openDefault,
			target:      "https://example.com/x",
			wantProgram: "rundll32",
			wantArgs:    []string{"url.dll,FileProtocolHandler", "https://example.com/x"},
		},
		{
			name: "windows opens a file", goos: "windows", mode: openDefault,
			target:      `C:\tmp\a.txt`,
			wantProgram: "rundll32",
			wantArgs:    []string{"url.dll,FileProtocolHandler", `C:\tmp\a.txt`},
		},
		{
			name: "unknown goos falls back to xdg-open", goos: "freebsd",
			mode: openDefault, target: "/tmp/a.txt",
			wantProgram: "xdg-open", wantArgs: []string{"/tmp/a.txt"},
		},
		{
			name: "darwin reveals", goos: "darwin", mode: reveal,
			target:      "/tmp/a.txt",
			wantProgram: "open", wantArgs: []string{"-R", "/tmp/a.txt"},
		},
		{
			name: "windows reveals", goos: "windows", mode: reveal,
			target:      `C:\tmp\a.txt`,
			wantProgram: "explorer",
			wantArgs:    []string{"/select,", `C:\tmp\a.txt`},
		},
		{
			name: "linux reveals the containing directory", goos: "linux",
			mode: reveal, target: "/tmp/a.txt",
			wantProgram: "xdg-open", wantArgs: []string{filepath.Dir("/tmp/a.txt")},
		},
		{
			name: "darwin open-as", goos: "darwin", mode: openAs,
			target:      "/tmp/a.txt",
			wantProgram: "open", wantArgs: []string{"/tmp/a.txt"},
		},
		{
			name: "linux open-as", goos: "linux", mode: openAs,
			target:      "/tmp/a.txt",
			wantProgram: "xdg-open", wantArgs: []string{"/tmp/a.txt"},
		},
		{
			name: "windows open-as", goos: "windows", mode: openAs,
			target:      `C:\tmp\a.txt`,
			wantProgram: "rundll32",
			wantArgs:    []string{"shell32.dll,OpenAs_RunDLL", `C:\tmp\a.txt`},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			program, args := openArgs(tc.goos, tc.mode, tc.target)
			if program != tc.wantProgram {
				t.Fatalf("program = %q, want %q", program, tc.wantProgram)
			}
			if !reflect.DeepEqual(args, tc.wantArgs) {
				t.Fatalf("args = %q, want %q", args, tc.wantArgs)
			}
		})
	}
}

func TestOpenWithHandsTheTableToLaunch(t *testing.T) {
	restore := launch
	t.Cleanup(func() { launch = restore })
	var gotProgram string
	var gotArgs []string
	launch = func(_ context.Context, program string, args []string) error {
		gotProgram, gotArgs = program, args
		return nil
	}
	if err := openWith(context.Background(), "windows", reveal, `C:\tmp\a.txt`); err != nil {
		t.Fatal(err)
	}
	if gotProgram != "explorer" {
		t.Fatalf("program = %q, want explorer", gotProgram)
	}
	if !reflect.DeepEqual(gotArgs, []string{"/select,", `C:\tmp\a.txt`}) {
		t.Fatalf("args = %q", gotArgs)
	}
}

func TestOpenWithPropagatesLaunchFailure(t *testing.T) {
	restore := launch
	t.Cleanup(func() { launch = restore })
	wantErr := errors.New("no launcher")
	launch = func(context.Context, string, []string) error { return wantErr }
	if err := openWith(context.Background(), "darwin", openDefault, "/tmp/a.txt"); !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
}
