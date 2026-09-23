package procmem

import (
	"os"
	"path/filepath"
	"testing"
)

// writeProcFS lays out a two-process /proc mount: 4242 names itself with
// spaces and parentheses and reports a smaps_rollup, 4243 has no cmdline
// and no smaps_rollup at all (an old kernel), so it falls back to statm.
func writeProcFS(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(path, body string) {
		t.Helper()
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", full, err)
		}
	}
	write("4242/stat", "4242 (my (odd) process) S 1 4242 4242 0 -1 4194304\n")
	write("4242/cmdline", "/usr/lib/webkit2gtk/WebKitWebProcess\x00--foo\x00")
	write("4242/smaps_rollup",
		"00400000-00401000 r-xp 00000000 08:01 1 /usr/bin/x\n"+
			"Rss:              1000 kB\n"+
			"Pss:               800 kB\n"+
			"Shared_Clean:      100 kB\n"+
			"Shared_Dirty:       50 kB\n")
	write("4243/stat", "4243 (opencraft) S 4242 4243 4243 0 -1 4194304\n")
	write("4243/statm", "1000 250 100 0 0 0 0\n")
	return root
}

func TestReadProcFS(t *testing.T) {
	records := readProcFS(writeProcFS(t))
	if len(records) != 2 {
		t.Fatalf("records = %d, want 2: %+v", len(records), records)
	}
	first := records[0]
	if first.pid != 4242 || first.ppid != 1 {
		t.Errorf("first record = %+v, want pid 4242 with ppid 1", first)
	}
	if first.name != "WebKitWebProcess" {
		t.Errorf("name = %q, want the argv[0] base name WebKitWebProcess", first.name)
	}
	second := records[1]
	if second.pid != 4243 || second.ppid != 4242 {
		t.Errorf("second record = %+v, want pid 4243 with ppid 4242", second)
	}
	if second.name != "opencraft" {
		t.Errorf("name = %q, want the comm fallback opencraft", second.name)
	}
}

func TestParseProcStatKeepsParensInTheName(t *testing.T) {
	ppid, name, ok := parseProcStat("1234 (a (b) c) R 999 1234 1234 0 -1 4194304")
	if !ok {
		t.Fatal("parseProcStat rejected a valid stat line")
	}
	if ppid != 999 {
		t.Errorf("ppid = %d, want 999", ppid)
	}
	if name != "a (b) c" {
		t.Errorf("name = %q, want %q", name, "a (b) c")
	}
	if _, _, ok := parseProcStat("garbage"); ok {
		t.Error("parseProcStat accepted a stat line without a name")
	}
}

func TestProcMemoryPrefersPss(t *testing.T) {
	root := writeProcFS(t)
	footprint, resident, ok := procMemory(root, "4242")
	if !ok {
		t.Fatal("procMemory found nothing for a process with a smaps_rollup")
	}
	if footprint != 800*1024 {
		t.Errorf("footprint = %d, want the Pss value 819200", footprint)
	}
	if resident != 1000*1024 {
		t.Errorf("resident = %d, want the Rss value 1024000", resident)
	}
}

func TestProcMemoryFallsBackToStatm(t *testing.T) {
	root := writeProcFS(t)
	footprint, resident, ok := procMemory(root, "4243")
	if !ok {
		t.Fatal("procMemory found nothing for a process with a statm")
	}
	want := uint64(250 * os.Getpagesize())
	if resident != want {
		t.Errorf("resident = %d, want %d", resident, want)
	}
	if footprint != want {
		t.Errorf("footprint = %d, want the resident size %d", footprint, want)
	}
	if _, _, ok := procMemory(root, "9999"); ok {
		t.Error("procMemory reported memory for a process that is not in the mount")
	}
}

func TestExecPathReadsTheSymlink(t *testing.T) {
	root := writeProcFS(t)
	full := filepath.Join(root, "4242", "exe")
	if err := os.Symlink("/usr/lib/webkit2gtk/WebKitWebProcess", full); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if got := execPath(root, "4242"); got != "/usr/lib/webkit2gtk/WebKitWebProcess" {
		t.Errorf("execPath = %q, want the symlink target", got)
	}
	if got := execPath(root, "4243"); got != "" {
		t.Errorf("execPath = %q, want empty for a process without an exe link", got)
	}
}

func TestReadProcFSIgnoresUnreadableEntries(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "self"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// A kernel thread has no stat line to read and a non-numeric entry is
	// no pid: neither may abort the walk.
	if err := os.WriteFile(filepath.Join(root, "self", "stat"), []byte("garbage"), 0o644); err != nil {
		t.Fatalf("write stat: %v", err)
	}
	records := readProcFS(filepath.Join(root, "missing"))
	if len(records) != 0 {
		t.Fatalf("records = %d, want none for a missing mount", len(records))
	}
	records = readProcFS(root)
	if len(records) != 0 {
		t.Fatalf("records = %d, want none for a mount of non-numeric entries", len(records))
	}
}
