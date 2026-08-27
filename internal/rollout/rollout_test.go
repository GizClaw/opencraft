package rollout

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
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

func readLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimRight(string(data), "\n"), "\n")
}
