// Package commands owns the slash command corpus: the command
// definitions and the BM25 index the TUI palette searches. Execution
// stays in the parent package, because command handlers need the TUI
// Model.
package commands

import "github.com/GizClaw/opencraft/internal/utils/search"

// Command is one slash command. Name is without the leading "/";
// Desc is both the palette subtitle and part of the search text.
type Command struct {
	Name string
	Desc string
}

// all is the single source of truth for /-commands.
var all = []Command{
	{
		Name: "resume",
		Desc: "pick and resume a past conversation",
	},
	{
		Name: "permissions",
		Desc: "switch the sandbox permission mode (workspace | yolo)",
	},
}

// List returns a copy of every registered command.
func List() []Command {
	out := make([]Command, len(all))
	copy(out, all)
	return out
}

// Lookup returns the command registered under name.
func Lookup(name string) (*Command, bool) {
	for i := range all {
		if all[i].Name == name {
			return &all[i], true
		}
	}
	return nil, false
}

// Result is one BM25 match from an Index search.
type Result struct {
	Name  string
	Score float64
}

// Index is a read-only BM25 index over the command corpus, matching
// both command names and descriptions.
type Index struct {
	ix *search.Index
}

// NewIndex builds the command index from the registered commands.
func NewIndex() *Index {
	docs := make([]search.Doc, 0, len(all))
	for _, c := range all {
		docs = append(docs, search.Doc{
			ID:   c.Name,
			Name: c.Name,
			Text: c.Desc,
		})
	}
	return &Index{ix: search.NewIndex(docs)}
}

// Search ranks commands against the query.
func (x *Index) Search(query string, limit int) []Result {
	hits := x.ix.Search(query, limit)
	out := make([]Result, 0, len(hits))
	for _, h := range hits {
		out = append(out, Result{Name: h.ID, Score: h.Score})
	}
	return out
}
