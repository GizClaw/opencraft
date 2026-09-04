package rollout

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRecorderAppendsAndContinuesSeq(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s-1", "rollout.jsonl")
	rec, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := rec.Record(Event{Type: TypeTurnStarted, RunID: "r1"}); err != nil {
		t.Fatal(err)
	}
	if err := rec.Record(Event{Type: TypeTurnCompleted, Status: "completed"}); err != nil {
		t.Fatal(err)
	}
	if err := rec.Close(); err != nil {
		t.Fatal(err)
	}

	rec2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rec2.Close() }()
	if err := rec2.Record(Event{Type: TypeTurnStarted, RunID: "r2"}); err != nil {
		t.Fatal(err)
	}

	lines := readLines(t, path)
	if len(lines) != 3 {
		t.Fatalf("lines = %d, want 3", len(lines))
	}
	for i, want := range []int64{0, 1, 2} {
		var ev Event
		if err := json.Unmarshal([]byte(lines[i]), &ev); err != nil {
			t.Fatalf("line %d: %v", i, err)
		}
		if ev.Seq != want {
			t.Fatalf("line %d seq = %d, want %d", i, ev.Seq, want)
		}
		if ev.Time == "" {
			t.Fatalf("line %d missing time", i)
		}
	}
}

func TestRecorderConcurrentWrites(t *testing.T) {
	rec, err := Open(filepath.Join(t.TempDir(), "s-1", "rollout.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rec.Close() }()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = rec.Record(Event{Type: TypeItemToolCall, Tool: "exec_command"})
		}()
	}
	wg.Wait()
	if lines := readLines(t, rec.Path()); len(lines) != 20 {
		t.Fatalf("lines = %d, want 20", len(lines))
	}
}

func TestRecorderRotatesAtCap(t *testing.T) {
	old := maxRolloutMB
	maxRolloutMB = 1
	t.Cleanup(func() { maxRolloutMB = old })

	path := filepath.Join(t.TempDir(), "s-1", "rollout.jsonl")
	rec, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	// ~40 KB per event keeps the record count low while crossing the
	// 1 MiB rotation threshold.
	for i := 0; i < 30; i++ {
		ev := Event{
			Type:      TypeItemToolCall,
			Tool:      "exec_command",
			Arguments: json.RawMessage(`"` + strings.Repeat("x", 40<<10) + `"`),
		}
		if err := rec.Record(ev); err != nil {
			t.Fatal(err)
		}
	}
	if err := rec.Close(); err != nil {
		t.Fatal(err)
	}

	lines := readLines(t, path)
	if len(lines) == 0 {
		t.Fatal("live rollout empty after rotation")
	}
	// Sequences stay unique and increasing across the rotation.
	var last int64 = -1
	for _, line := range lines {
		var ev Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatal(err)
		}
		if ev.Seq <= last {
			t.Fatalf("seq not increasing: %d after %d", ev.Seq, last)
		}
		last = ev.Seq
	}
	// lumberjack keeps one timestamped backup; removal can lag behind
	// rotation, so poll briefly.
	deadline := time.Now().Add(2 * time.Second)
	pattern := filepath.Join(filepath.Dir(path),
		strings.TrimSuffix(filepath.Base(path), ".jsonl")+"-*.jsonl")
	backups, err := filepath.Glob(pattern)
	for len(backups) == 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		backups, err = filepath.Glob(pattern)
	}
	if err != nil || len(backups) == 0 {
		t.Fatalf("no rotated backup found: %v", err)
	}
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimRight(string(data), "\n"), "\n")
}
