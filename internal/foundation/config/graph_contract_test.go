package config

import (
	"strings"
	"testing"

	"github.com/GizClaw/opencraft/internal/foundation/utils/summarytext"
)

// TestCompactArchiveChannelMatchesGraphAsset pins the board channel the
// compact node moves folded conversation onto. The graph asset is a JS
// script that cannot import the Go constant, so the literal is checked
// here: a rename on either side would silently split the model view
// from the durable turn archive.
func TestCompactArchiveChannelMatchesGraphAsset(t *testing.T) {
	js := readAsset(t, "assets/graphs/nodes/compact.js")
	if !strings.Contains(js, `"`+CompactArchiveChannel+`"`) {
		t.Fatalf("compact.js does not reference %q; the memory hooks read that channel",
			CompactArchiveChannel)
	}
}

// TestContextNoticePrefixMatchesGraphAsset pins the other literal the
// compact node and Go share. The node writes a notice carrying this
// prefix when automatic compaction is out of options; the memory hooks
// drop notices from the persisted conversation by the same prefix. A
// rename on one side would persist harness text as the user's own words
// (or drop real conversation), and neither failure is visible in tests
// that only exercise one side.
func TestContextNoticePrefixMatchesGraphAsset(t *testing.T) {
	js := readAsset(t, "assets/graphs/nodes/compact.js")
	if !strings.Contains(js, `"`+summarytext.ContextNoticePrefix+`"`) {
		t.Fatalf("compact.js does not write %q; the notice filter in memory/hooks.go reads it",
			summarytext.ContextNoticePrefix)
	}
	if !strings.Contains(js, "NOTICE_PREFIX") {
		t.Fatal("compact.js must build the notice through NOTICE_PREFIX")
	}
}

// TestCompactionBoardVarsMatchGraphAssets pins the board vars the compact
// node exchanges with Go against the names Go reads. The Go side is
// single-sourced in graph_contract.go (a typo there is a compile error at
// the read site), so what this test catches is the other direction: a rename
// in the asset, which would silently drop the anchor's measurement and the
// UI's compaction notice.
func TestCompactionBoardVarsMatchGraphAssets(t *testing.T) {
	compact := readAsset(t, "assets/graphs/nodes/compact.js")
	for _, name := range CompactionBoardVars {
		if !strings.Contains(compact, `"`+name+`"`) {
			t.Fatalf("compact.js never mentions %q, which Go reads", name)
		}
	}
}

// TestWorldNodeMergesThroughItsGoBridge pins the split between the
// graph's world node and the merge itself: the node seeds the channel
// through the "ocworld" global (worldstate.Merge), never through the
// board bridge. board.channel() and board.setChannel() project every
// message through JSON in each direction, and the world node rewrites
// the whole transcript every turn — doing that from script is what made
// a long conversation pay ~70 ms per turn at 2 MB, ~280 ms at 8 MB,
// before the model saw a token. The node staying a one-line entry point
// is the intended shape, not an accident of the port.
//
// The board vars the merge reads are Go-only now (writer and reader are
// both internal/capabilities/worldstate), so a rename there is a
// compile error rather than something this test has to catch. The
// global's name is checked by the e2e test that runs this graph.
func TestWorldNodeMergesThroughItsGoBridge(t *testing.T) {
	js := readAsset(t, "assets/graphs/nodes/world.js")
	if !strings.Contains(js, "ocworld.merge(") {
		t.Fatal("world.js does not call ocworld.merge")
	}
	for _, forbidden := range []string{"board.channel(", "board.setChannel("} {
		if strings.Contains(js, forbidden) {
			t.Fatalf("world.js calls %s: the channel must not cross the script bridge",
				forbidden)
		}
	}
}

// TestCompactNodeSizesThroughItsGoBridge pins the split between the
// graph's compaction node and the estimate: the node decides on
// "occompact" (capabilities/compaction), never on a local copy of the
// rule. Mirroring summarytext in JS was a per-character CJK scan through
// goja, ~80x the native number: 62 ms per sizing pass on a 230 KB
// channel, 171 ms on a 560 KB one, and the node sizes twice a round. It
// also needed a shared fixture to keep two copies of one rule in step.
// The node keeps the policy (when to fold, which slice, how the result
// lands) and calls Go only for the number.
func TestCompactNodeSizesThroughItsGoBridge(t *testing.T) {
	js := readAsset(t, "assets/graphs/nodes/compact.js")
	if !strings.Contains(js, "occompact.estimate(") {
		t.Fatal("compact.js does not size the channel through occompact.estimate")
	}
	for _, forbidden := range []string{"charCodeAt(", "JSON.stringify("} {
		if strings.Contains(js, forbidden) {
			t.Fatalf("compact.js still renders text through %s: the estimate "+
				"is Go's rule, and a second renderer drifts from it", forbidden)
		}
	}
}

// TestCompactWritesRoutingVarsTheGraphTests pins the routing contract
// between the compact node and the graph definition. The edges test bare
// board vars ("tool_pending == false"), and a node that writes the same
// fact under a longer name (world.tool_pending) leaves those conditions
// reading an undefined variable: both edges fail and the split falls
// through to its default edge, which is __end__. The turn then completes
// without ever calling the model — silently, with a zero-token result.
func TestCompactWritesRoutingVarsTheGraphTests(t *testing.T) {
	graph := readAsset(t, "assets/graphs/assistant.yaml")
	js := readAsset(t, "assets/graphs/nodes/compact.js")
	for _, want := range []string{"tool_pending == false", "tool_pending == true"} {
		if !strings.Contains(graph, want) {
			t.Fatalf("assistant.yaml no longer routes on %q; update this test", want)
		}
	}
	if !strings.Contains(js, `board.setVar("tool_pending"`) {
		t.Fatal("compact.js must write tool_pending under the name the graph tests")
	}
	if strings.Contains(js, `board.setVar("world.tool_pending"`) {
		t.Fatal("compact.js writes world.tool_pending; the graph edges read tool_pending")
	}
}

// TestCompactionBoardVarsAreNamespaced keeps every var where the engine and
// the node agree they live: world.compact.* and world.* belong to the
// assistant graph's world state, llm_usage is the node-level usage slot, and
// nothing may enter the engine's reserved "__" namespace.
func TestCompactionBoardVarsAreNamespaced(t *testing.T) {
	for _, name := range append(
		[]string{BoardVarTailBlock, BoardVarContextUsage}, CompactionBoardVars...) {
		if strings.HasPrefix(name, "__") {
			t.Fatalf("board var %q uses the engine's reserved namespace", name)
		}
		switch {
		case strings.HasPrefix(name, "world."), name == BoardVarLLMUsage:
		default:
			t.Fatalf("board var %q is outside the world.* / llm_usage contract", name)
		}
	}
}

func readAsset(t *testing.T, path string) string {
	t.Helper()
	data, err := FS().ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
