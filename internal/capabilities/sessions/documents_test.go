package sessions

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/GizClaw/opencraft/internal/capabilities/sessions/state"
)

// TestDocumentNamesAreWireValues pins the names to the keys already in
// the database. A rename here is a migration: the document a build
// reads is the document some earlier build wrote, and a reader that
// silently looks under a new key loses a user's rename, plan or
// settings without an error.
func TestDocumentNamesAreWireValues(t *testing.T) {
	for _, tc := range []struct{ name, got, want string }{
		{"DocumentTitle", DocumentTitle, "title"},
		{"DocumentSettings", DocumentSettings, "settings"},
		{"DocumentPlans", DocumentPlans, "plans"},
		{"DocumentSkillActivations", DocumentSkillActivations, "skill_activations"},
		{"DocumentUsageAnchor", DocumentUsageAnchor, "usage_anchor"},
		{"DocumentCompact", DocumentCompact, "compact"},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %q, want the stored key %q",
				tc.name, tc.got, tc.want)
		}
	}
}

// TestDocumentsRegistryIsComplete keeps the registry in documents.go
// equal to the constants beside it: every Document* constant is listed
// once, with an owner and a generation, and nothing else is. Without
// this, adding a document to the constants and forgetting the registry
// (or the reverse) is invisible — the registry is what a future
// migration and docs/session-data-model.md §2 walk.
func TestDocumentsRegistryIsComplete(t *testing.T) {
	declared := documentConstants(t)

	listed := make(map[string]bool, len(Documents))
	for _, doc := range Documents {
		if listed[doc.Name] {
			t.Errorf("document %q listed twice", doc.Name)
		}
		listed[doc.Name] = true
		if !declared[doc.Name] {
			t.Errorf("registry lists %q, which no Document* constant declares",
				doc.Name)
		}
		if strings.TrimSpace(doc.Owner) == "" {
			t.Errorf("document %q has no owner", doc.Name)
		}
		if doc.Generation < 1 {
			t.Errorf("document %q has generation %d, want >= 1",
				doc.Name, doc.Generation)
		}
	}
	for name := range declared {
		if !listed[name] {
			t.Errorf("constant for document %q is missing from Documents", name)
		}
	}
}

// TestDocumentNamesAreNotSpelledAtCallSites is the drift guard for the
// registry: every read and write of a conversation_state document names
// it through a Document* constant, so "which documents exist" has one
// answer. A string literal passed as the document name is exactly the
// scattering this registry replaced — the compiler cannot see it and
// neither can a reader.
//
// The one exception is internal/foundation/compat: a migration has to
// keep reading and writing the shapes of builds that are gone, with
// literals frozen at the time that build shipped (see
// compat/settingsmerge.go). Test files are skipped too: a test that
// spells "plans" is pinning the wire name on purpose.
func TestDocumentNamesAreNotSpelledAtCallSites(t *testing.T) {
	root := moduleRoot(t)
	skipDirs := map[string]bool{
		".git": true, ".opencraft": true, "frontend": true,
		"node_modules": true, "build": true, "dist": true, "vendor": true,
	}
	// Methods that take a document name, and the argument index it
	// sits at: store.ReadState(id, name, v) vs
	// state.GetConversationState(ctx, id, name).
	nameArg := map[string]int{
		"ReadState":            1,
		"ReadStateStrict":      1,
		"WriteState":           1,
		"GetConversationState": 2,
		"SetConversationState": 2,
	}

	fset := token.NewFileSet()
	walkErr := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if skipDirs[entry.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") ||
			strings.HasSuffix(path, "_test.go") ||
			strings.Contains(filepath.ToSlash(path), "/foundation/compat/") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !strings.Contains(string(data), "ReadState") &&
			!strings.Contains(string(data), "WriteState") &&
			!strings.Contains(string(data), "ConversationState") {
			return nil
		}
		file, err := parser.ParseFile(fset, path, data, 0)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			rel = path
		}
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			idx, ok := nameArg[sel.Sel.Name]
			if !ok || idx >= len(call.Args) {
				return true
			}
			lit, ok := call.Args[idx].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			name, err := strconv.Unquote(lit.Value)
			if err != nil {
				name = lit.Value
			}
			t.Errorf("%s:%d: conversation_state document %q is spelled as a "+
				"literal; use the sessions.Document* constant",
				filepath.ToSlash(rel), fset.Position(lit.Pos()).Line, name)
			return true
		})
		return nil
	})
	if walkErr != nil {
		t.Fatalf("scan for document name literals: %v", walkErr)
	}
}

// externalDocumentNames maps a Document* constant whose value lives in
// another package to that value. DocumentSettings is the only one: the
// settings document's name belongs to the state package, which owns and
// reads the document. Teaching the test about a new one is deliberate —
// a name that comes from elsewhere is exactly what a reader cannot see
// in this file.
var externalDocumentNames = map[string]string{
	"SessionSettingsName": state.SessionSettingsName,
}

// documentConstants returns the document names declared as Document*
// constants in documents.go, by value.
func documentConstants(t *testing.T) map[string]bool {
	t.Helper()
	path := filepath.Join(moduleRoot(t), "internal", "capabilities",
		"sessions", "documents.go")
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	names := map[string]bool{}
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, ident := range value.Names {
				if !strings.HasPrefix(ident.Name, "Document") ||
					i >= len(value.Values) {
					continue
				}
				switch expr := value.Values[i].(type) {
				case *ast.BasicLit:
					if expr.Kind != token.STRING {
						t.Fatalf("document constant %s is not a string",
							ident.Name)
					}
					name, err := strconv.Unquote(expr.Value)
					if err != nil {
						t.Fatalf("document constant %s: %v", ident.Name, err)
					}
					names[name] = true
				case *ast.SelectorExpr:
					name, ok := externalDocumentNames[expr.Sel.Name]
					if !ok {
						t.Fatalf("document constant %s = %s, which "+
							"externalDocumentNames does not resolve",
							ident.Name, expr.Sel.Name)
					}
					names[name] = true
				default:
					t.Fatalf("document constant %s is neither a literal "+
						"nor a known package constant", ident.Name)
				}
			}
		}
	}
	if len(names) == 0 {
		t.Fatalf("no Document* constants found in %s", path)
	}
	return names
}

// moduleRoot walks up from this test file to the directory holding
// go.mod.
func moduleRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the test file")
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no go.mod above %s", file)
		}
		dir = parent
	}
}
