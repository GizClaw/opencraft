package config

import (
	"regexp"
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

// TestCompactionBoardVarsMatchGraphAssets pins the board vars the graph's
// script nodes exchange with Go against the names Go reads. The Go side is
// single-sourced in graph_contract.go (a typo there is a compile error at
// the read site), so what this test catches is the other direction: a rename
// in the asset, which would silently drop the anchor's measurement and the
// UI's compaction notice.
func TestCompactionBoardVarsMatchGraphAssets(t *testing.T) {
	compact := readAsset(t, "assets/graphs/nodes/compact.js")
	world := readAsset(t, "assets/graphs/nodes/world.js")
	for _, name := range CompactionBoardVars {
		if !strings.Contains(compact, `"`+name+`"`) {
			t.Fatalf("compact.js never mentions %q, which Go reads", name)
		}
	}
	if !strings.Contains(world, `"`+BoardVarTailBlock+`"`) {
		t.Fatalf("world.js never reads %q, which Go writes", BoardVarTailBlock)
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
	for _, name := range append([]string{BoardVarTailBlock}, CompactionBoardVars...) {
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

// TestTailBlockVarIsNotAWireName keeps the writer honest: the tail block is
// board state, never a message of its own on the channel.
func TestTailBlockVarIsNotAWireName(t *testing.T) {
	js := readAsset(t, "assets/graphs/nodes/world.js")
	if strings.Contains(js, "role: \""+BoardVarTailBlock+"\"") {
		t.Fatal("the tail block must not be used as a message role")
	}
	// The block is always user-role content: it carries the user's own turn
	// context, and the model must not read it as another agent's words.
	if !regexp.MustCompile(`role: "user"`).MatchString(js) {
		t.Fatal("world.js no longer emits a user-role message for the tail block")
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
