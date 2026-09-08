package gitx

import (
	"os/exec"
	"testing"
)

func TestParseRemote(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		host    string
		owner   string
		repo    string
		wantErr bool
	}{
		{name: "https with .git", raw: "https://github.com/octo/cat.git",
			host: "github.com", owner: "octo", repo: "cat"},
		{name: "https without .git",
			raw: "https://github.com/octo/cat", host: "github.com",
			owner: "octo", repo: "cat"},
		{name: "https trailing slash",
			raw: "https://github.com/octo/cat/", host: "github.com",
			owner: "octo", repo: "cat"},
		{name: "scp syntax", raw: "git@github.com:octo/cat.git",
			host: "github.com", owner: "octo", repo: "cat"},
		{name: "ssh url", raw: "ssh://git@github.com/octo/cat.git",
			host: "github.com", owner: "octo", repo: "cat"},
		{name: "https with user", raw: "https://user@github.com/octo/cat",
			host: "github.com", owner: "octo", repo: "cat"},
		{name: "host with port", raw: "ssh://git@github.com:2222/octo/cat.git",
			host: "github.com", owner: "octo", repo: "cat"},
		{name: "non github host",
			raw: "git@git.example.com:octo/cat.git", host: "git.example.com",
			owner: "octo", repo: "cat"},
		{name: "repo with dashes and dots",
			raw:  "https://github.com/octo/cat.food-bowl.git",
			host: "github.com", owner: "octo", repo: "cat.food-bowl"},
		{name: "local path", raw: "/srv/git/cat.git", wantErr: true},
		{name: "file url", raw: "file:///srv/git/cat.git", wantErr: true},
		{name: "single segment", raw: "https://github.com/octo", wantErr: true},
		{name: "empty", raw: "", wantErr: true},
		{name: "injection slash", raw: "https://github.com/octo/../evil", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseRemote(tc.raw)
			if tc.wantErr {
				if ok {
					t.Fatalf("parseRemote(%q) = %+v, want error", tc.raw, got)
				}
				return
			}
			if !ok {
				t.Fatalf("parseRemote(%q) reported not parseable", tc.raw)
			}
			if got.Host != tc.host || got.Owner != tc.owner ||
				got.Repo != tc.repo {
				t.Fatalf("parseRemote(%q) = %+v, want %s/%s on %s",
					tc.raw, got, tc.owner, tc.repo, tc.host)
			}
		})
	}
}

func TestRemoteFromRepository(t *testing.T) {
	root := t.TempDir()
	initRepo(t, root)
	run := exec.Command("git", "-C", root, "remote", "add", "origin",
		"git@github.com:GizClaw/opencraft.git")
	if out, err := run.CombinedOutput(); err != nil {
		t.Fatalf("git remote add: %v\n%s", err, out)
	}
	info, ok := Remote(t.Context(), root)
	if !ok {
		t.Fatal("Remote() reported no origin remote")
	}
	if info.Host != "github.com" || info.Owner != "GizClaw" ||
		info.Repo != "opencraft" {
		t.Fatalf("Remote() = %+v", info)
	}
}
