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
		// A named "." is the process's own directory: a caller that
		// wrote "." named it, and the guard above is about blank
		// input, which must not be cleaned into this.
		{"explicit dot", ".", Target{Kind: TargetWorkspace, ID: "."}},
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

// TestSameTarget pins the comparison every layer is supposed to ask
// instead of cleaning paths by hand: it answers for the pool's identity
// rule, so two spellings it reports equal are also the same key.
func TestSameTarget(t *testing.T) {
	cases := []struct {
		name string
		a, b Target
		want bool
	}{
		{"same workspace", WorkspaceTarget("/x/y"), WorkspaceTarget("/x/y"), true},
		{"equivalent spelling", WorkspaceTarget("/x/y/"), WorkspaceTarget("/x/y"), true},
		{"dot segments", WorkspaceTarget("/x/./y"), WorkspaceTarget("/x/y"), true},
		{"trailing space is a different path",
			WorkspaceTarget("/x/y "), WorkspaceTarget("/x/y"), false},
		{"different workspace", WorkspaceTarget("/x/y"), WorkspaceTarget("/x/z"), false},
		{"same id, other scope", WorkspaceTarget("/x/y"), AppTarget("/x/y"), false},
		{"same app", AppTarget("demo"), AppTarget("demo"), true},
		{"invalid borrows nothing", Target{}, WorkspaceTarget("/x/y"), false},
		{"invalid matches nothing", Target{}, Target{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SameTarget(tc.a, tc.b); got != tc.want {
				t.Fatalf("SameTarget(%s, %s) = %v, want %v",
					tc.a, tc.b, got, tc.want)
			}
			// SameTarget is the pool's rule, so equal targets must
			// also be one pool key.
			if tc.want && tc.a.Key() != tc.b.Key() {
				t.Fatalf("SameTarget says %s and %s are one thing, "+
					"but they key differently", tc.a, tc.b)
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
	// The same string in both scopes is the pair a key that forgot the
	// kind cannot tell apart, so it is asserted on its own: an app id
	// and a directory may literally be the same text.
	sameID := "/apps/werewolf"
	if WorkspaceTarget(sameID).Key() == AppTarget(sameID).Key() {
		t.Fatalf("one id in two scopes shares a pool key: %q", sameID)
	}
	// And the format itself, separator and order included: this is the
	// only place that says the kind is a prefix, which is what keeps
	// the two scopes out of one slot.
	if got, want := WorkspaceTarget("/a").Key(), "ws\x00/a"; got != want {
		t.Fatalf("workspace key = %q, want %q", got, want)
	}
	if got, want := AppTarget("demo").Key(), "app\x00demo"; got != want {
		t.Fatalf("app key = %q, want %q", got, want)
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
