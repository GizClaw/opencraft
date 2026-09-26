package procmem

import "testing"

// familyRecords is a small pid tree: root 100 spawns an execd child 101,
// which spawns a shell 102, and an MCP server 103. 200 is an unrelated
// process, and 300..304 are platform helpers reparented to pid 1 — the
// ownership API is the only thing that connects them to 100.
func familyRecords() []record {
	return []record{
		{pid: 100, ppid: 1, name: "opencraft"},
		{pid: 101, ppid: 100, name: "opencraft"},
		{pid: 102, ppid: 101, name: "sh"},
		{pid: 103, ppid: 100, name: "node"},
		{pid: 200, ppid: 1, name: "unrelated"},
		{pid: 300, ppid: 1, name: "com.apple.WebKit.WebContent"},
		{pid: 301, ppid: 1, name: "com.apple.WebKit.GPU"},
		{pid: 302, ppid: 1, name: "com.apple.WebKit.Networking"},
		{pid: 303, ppid: 1, name: "SetStoreUpdateService"},
		{pid: 304, ppid: 1, name: "someone-elses-helper"},
	}
}

func familyOwners() map[int]int {
	return map[int]int{
		100: 100,
		101: 100,
		102: 100,
		103: 100,
		300: 100,
		301: 100,
		302: 100,
		303: 100,
		304: 999,
	}
}

func TestAssembleIncludesOwnedHelpers(t *testing.T) {
	family := assemble(100, familyRecords(), familyOwners())
	if family.Root != 100 {
		t.Fatalf("root = %d, want 100", family.Root)
	}
	if family.Source != SourceResponsibility {
		t.Fatalf("source = %q, want %q", family.Source, SourceResponsibility)
	}
	want := []struct {
		pid  int
		role Role
	}{
		{100, RoleSelf},
		{101, RoleChild},
		{102, RoleChild},
		{103, RoleChild},
		{300, RoleWebContent},
		{301, RoleGPU},
		{302, RoleNetworking},
		{303, RoleHelper},
	}
	if len(family.Members) != len(want) {
		t.Fatalf("members = %d, want %d: %+v", len(family.Members), len(want), family.Members)
	}
	for i, want := range want {
		member := family.Members[i]
		if member.PID != want.pid {
			t.Fatalf("member %d = pid %d, want %d (members must be ordered by pid)",
				i, member.PID, want.pid)
		}
		if member.Role != want.role {
			t.Errorf("pid %d role = %q, want %q", member.PID, member.Role, want.role)
		}
	}
}

func TestAssembleWithoutOwnershipKeepsTheTree(t *testing.T) {
	family := assemble(100, familyRecords(), nil)
	if family.Source != SourceTree {
		t.Fatalf("source = %q, want %q", family.Source, SourceTree)
	}
	if len(family.Members) != 4 {
		t.Fatalf("members = %d, want the tree only: %+v", len(family.Members), family.Members)
	}
	for _, member := range family.Members {
		if member.PID == 100 {
			if member.Role != RoleSelf {
				t.Errorf("self role = %q, want %q", member.Role, RoleSelf)
			}
			continue
		}
		if member.Role != RoleChild {
			t.Errorf("pid %d role = %q, want %q", member.PID, member.Role, RoleChild)
		}
	}
}

// A process the ownership API reports as someone else's must not join the
// family, however it is named.
func TestAssembleIgnoresForeignOwners(t *testing.T) {
	family := assemble(100, familyRecords(), familyOwners())
	if _, ok := family.Member(304); ok {
		t.Fatalf("a process owned by another pid joined the family: %+v", family.Members)
	}
}

func TestFamilyTotalSumsFootprints(t *testing.T) {
	family := Family{Members: []Process{
		{PID: 1, Footprint: 100},
		{PID: 2, Footprint: 250},
	}}
	if total := family.Total(); total != 350 {
		t.Fatalf("Total = %d, want 350", total)
	}
}

func TestRoleFor(t *testing.T) {
	tests := []struct {
		name       string
		descendant bool
		want       Role
	}{
		{"opencraft", false, RoleHelper},
		{"com.apple.WebKit.WebContent", false, RoleWebContent},
		{"WebKitWebProcess", false, RoleWebContent},
		{"com.apple.WebKit.GPU", false, RoleGPU},
		{"WebKitGPUProcess", false, RoleGPU},
		{"com.apple.WebKit.Networking", false, RoleNetworking},
		{"WebKitNetworkProcess", false, RoleNetworking},
		{"com.apple.audio.SandboxHelper", false, RoleHelper},
		// A descendant stays a child whatever it is called.
		{"com.apple.WebKit.WebContent", true, RoleChild},
	}
	for _, test := range tests {
		if got := roleFor(test.name, test.descendant); got != test.want {
			t.Errorf("roleFor(%q, %v) = %q, want %q",
				test.name, test.descendant, got, test.want)
		}
	}
}
