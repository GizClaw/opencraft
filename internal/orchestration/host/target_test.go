package host

import "testing"

func TestWorkspaceTargetCleansAndRefusesBlank(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  Target
	}{
		{"blank", "", Target{}},
		{"spaces", "   ", Target{}},
		{"cleaned", "/home/me/project/", Target{Kind: TargetWorkspace, ID: "/home/me/project"}},
		{"dot segments", "/home/me/../me/project", Target{Kind: TargetWorkspace, ID: "/home/me/project"}},
		{"relative", "project", Target{Kind: TargetWorkspace, ID: "project"}},
		// A trailing space is part of a path, and trimming one would
		// silently point the pool at another directory.
		{"trailing space", "/home/me/project ", Target{Kind: TargetWorkspace, ID: "/home/me/project "}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := WorkspaceTarget(tc.input); got != tc.want {
				t.Fatalf("WorkspaceTarget(%q) = %+v, want %+v", tc.input, got, tc.want)
			}
		})
	}
}

func TestAppTargetTrimsAndRefusesBlank(t *testing.T) {
	if got := AppTarget(""); got != (Target{}) {
		t.Fatalf("AppTarget(\"\") = %+v, want the zero Target", got)
	}
	if got := AppTarget("  "); got != (Target{}) {
		t.Fatalf("AppTarget(\"  \") = %+v, want the zero Target", got)
	}
	want := Target{Kind: TargetApp, ID: "werewolf"}
	if got := AppTarget(" werewolf "); got != want {
		t.Fatalf("AppTarget = %+v, want %+v", got, want)
	}
}

func TestTargetValid(t *testing.T) {
	cases := []struct {
		target Target
		want   bool
	}{
		{Target{}, false},
		{Target{Kind: TargetWorkspace}, false},
		{Target{ID: "/home/me/project"}, false},
		{Target{Kind: TargetWorkspace, ID: "/home/me/project"}, true},
		{Target{Kind: TargetApp, ID: "werewolf"}, true},
		{Target{Kind: "elsewhere", ID: "werewolf"}, false},
	}
	for _, tc := range cases {
		if got := tc.target.Valid(); got != tc.want {
			t.Fatalf("%+v.Valid() = %v, want %v", tc.target, got, tc.want)
		}
	}
}

// One workspace and one application may share an ID — "werewolf" is a
// legal directory name — and the pool must still see two targets.
func TestTargetKeySeparatesScopes(t *testing.T) {
	ws := WorkspaceTarget("/apps/werewolf")
	app := AppTarget("werewolf")
	if ws.Key() == app.Key() {
		t.Fatalf("workspace and app share the pool key %q", ws.Key())
	}
	zero := Target{}
	if zero.Key() == ws.Key() || zero.Key() == app.Key() {
		t.Fatalf("zero Target shares a pool key with a real target")
	}
	if got := WorkspaceTarget("/home/me/project/").Key(); got != WorkspaceTarget("/home/me/project").Key() {
		t.Fatalf("equivalent workspace paths key differently: %q", got)
	}
}

func TestTargetString(t *testing.T) {
	cases := []struct {
		target Target
		want   string
	}{
		{Target{}, "target=<none>"},
		{Target{Kind: TargetWorkspace}, "target=<none>"},
		{WorkspaceTarget("/home/me/project"), "workspace=/home/me/project"},
		{AppTarget("werewolf"), "app=werewolf"},
	}
	for _, tc := range cases {
		if got := tc.target.String(); got != tc.want {
			t.Fatalf("%+v.String() = %q, want %q", tc.target, got, tc.want)
		}
	}
}
