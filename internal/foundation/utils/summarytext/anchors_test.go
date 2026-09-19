package summarytext

import (
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/message"
)

func TestExtractAnchorsKeepsIdentifiersAndSkipsNoise(t *testing.T) {
	msgs := []message.Message{
		message.NewTextMessage(message.RoleUser,
			"fix internal/capabilities/worldstate/worldstate.go and "+
				"cmd/opencraft/main.go (see #412, ABC-77, landed in 4f2a1bc)"),
		message.NewTextMessage(message.RoleAssistant,
			"the file is encoded UTF-8 and SHA-256 sums are in docs/notes.md"),
		message.NewTextMessage(message.RoleUser,
			"internal/capabilities/worldstate/worldstate.go again"),
	}
	idx := ExtractAnchors(msgs)
	want := []string{
		"internal/capabilities/worldstate/worldstate.go",
		"cmd/opencraft/main.go",
		"docs/notes.md",
	}
	if strings.Join(idx.Paths, "|") != strings.Join(want, "|") {
		t.Fatalf("paths = %v, want %v (first-seen order, deduped)", idx.Paths, want)
	}
	if len(idx.SHAs) != 1 || idx.SHAs[0] != "4f2a1bc" {
		t.Fatalf("shas = %v, want just the commit", idx.SHAs)
	}
	if len(idx.Refs) != 1 || idx.Refs[0] != "#412" {
		t.Fatalf("refs = %v, want just #412", idx.Refs)
	}
	if len(idx.Keys) != 1 || idx.Keys[0] != "ABC-77" {
		t.Fatalf("keys = %v, want just ABC-77 (UTF-8 / SHA-256 are denied)", idx.Keys)
	}
	rendered := idx.Render()
	for _, frag := range []string{
		"## Identifiers", "Files: internal/capabilities/worldstate/worldstate.go",
		"Commits: 4f2a1bc", "Issues/PRs: #412", "Tickets: ABC-77",
	} {
		if !strings.Contains(rendered, frag) {
			t.Fatalf("rendered index missing %q:\n%s", frag, rendered)
		}
	}
}

// TestAnchorIndexKeepsEarlierIdentifiers pins the re-fold path: a later
// fold's messages no longer contain the identifiers of an earlier one, so
// the previous summary's verbatim block is merged in rather than
// regenerated.
func TestAnchorIndexKeepsEarlierIdentifiers(t *testing.T) {
	idx := ExtractAnchors([]message.Message{
		message.NewTextMessage(message.RoleUser, "now touch internal/new/file.go"),
	})
	idx.AddText("## Identifiers (extracted verbatim from the folded messages)\n" +
		"Files: internal/old/file.go\nCommits: 0badc0de\n")
	if len(idx.Paths) != 2 ||
		idx.Paths[0] != "internal/new/file.go" ||
		idx.Paths[1] != "internal/old/file.go" {
		t.Fatalf("paths = %v, want the new path then the carried-over one", idx.Paths)
	}
	if len(idx.SHAs) != 1 || idx.SHAs[0] != "0badc0de" {
		t.Fatalf("shas = %v, want the carried-over commit", idx.SHAs)
	}
}

func TestAnchorIndexEmptyAndBounds(t *testing.T) {
	if got := ExtractAnchors(nil); !got.Empty() || got.Render() != "" {
		t.Fatalf("empty index = %+v / %q, want nothing", got, got.Render())
	}
	var msgs []message.Message
	for i := 0; i < anchorMaxItems*2; i++ {
		msgs = append(msgs,
			message.NewTextMessage(message.RoleUser, "internal/pkg/file"+string(rune('a'+i%26))+".go"))
	}
	idx := ExtractAnchors(msgs)
	if len(idx.Paths) > anchorMaxItems {
		t.Fatalf("paths = %d, want at most %d", len(idx.Paths), anchorMaxItems)
	}
	if out := idx.Render(); len(out) > anchorMaxChars+64 {
		t.Fatalf("rendered index = %d bytes, want it bounded", len(out))
	}
}
