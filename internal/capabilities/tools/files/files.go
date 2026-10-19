// Package files provides the workspace file tools: read_file,
// write_file, list_dir, grep, and glob. All paths are relative to the
// workspace root; absolute paths and ".." are rejected by the
// workspace contract.
package files

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/tool"
	"github.com/GizClaw/flowcraft/core/utils/filetype"
	"github.com/GizClaw/flowcraft/core/workspace"

	"github.com/GizClaw/opencraft/internal/capabilities/tools/toolargs"
	"github.com/GizClaw/opencraft/internal/foundation/platform/fshidden"
)

const (
	// ReadFileName is the canonical read_file tool name.
	ReadFileName = "read_file"
	// WriteFileName is the canonical write_file tool name.
	WriteFileName = "write_file"
	// ListDirName is the canonical list_dir tool name.
	ListDirName = "list_dir"
	// GrepName is the canonical grep tool name.
	GrepName = "grep"
	// GlobName is the canonical glob tool name.
	GlobName = "glob"
)

// ReadResultChars is the content budget of one read. The assembly's
// truncate middleware caps a result at middlewares.truncate.max_chars
// (24000 in tools.yaml) and answers anything longer with a head-and-tail
// excerpt plus a cache pointer. A window below that cap comes back
// whole, so a read is a complete range the model can page from, and
// next_offset says where the next one starts. Without the budget a read
// of a long file is cut twice — once at the line limit and again by the
// middleware — which leaves the model holding the two ends of a range
// whose middle it cannot reach in one call.
const ReadResultChars = 20_000

const (
	defaultReadLimit = 1000
	maxReadLimit     = 100_000
	maxReadFileBytes = 2 << 20 // 2 MiB: refuse pathological reads
	maxGrepFileBytes = 1 << 20 // 1 MiB: skip larger files in grep
	// binarySniffBytes is how far into a file grep looks for a NUL byte
	// before treating the file as binary — the rule GNU grep uses for
	// "binary file matches".
	binarySniffBytes   = 8 << 10
	defaultGrepMatches = 100
	defaultListDepth   = 4
	maxListDepth       = 32
	maxWalkEntries     = 10_000
	alwaysSkipDir      = ".git"
)

// The per-call budgets are variables so tests can exercise the caps
// cheaply: maxGrepFiles bounds how many files one grep scans when no
// match is found, maxGrepMatches how many matches it returns, and
// maxGlobMatches how many paths one glob reports. The walk is what
// costs, and the cap keeps a pattern that matches half a repository
// from handing the model a list it cannot use.
var (
	maxGrepFiles   = 5_000
	maxGrepMatches = 1000
	maxGlobMatches = 1000
)

// Tool is the workspace-backed file tool group.
type Tool struct {
	ws workspace.Workspace
}

// New creates the file tools over ws. ws is required.
func New(ws workspace.Workspace) (*Tool, error) {
	if ws == nil {
		return nil, errdefs.Validationf("files: workspace is required")
	}
	return &Tool{ws: ws}, nil
}

// MustNew panics on invalid construction; use in static wiring.
func MustNew(ws workspace.Workspace) *Tool {
	t, err := New(ws)
	if err != nil {
		panic(err)
	}
	return t
}

// Tools returns the five file tools sharing this workspace.
func (t *Tool) Tools() []tool.Tool {
	return []tool.Tool{
		&readFileTool{t.ws},
		&writeFileTool{t.ws},
		&listDirTool{t.ws},
		&grepTool{t.ws},
		&globTool{t.ws},
	}
}

// ---------------------------------------------------------------------------
// read_file
// ---------------------------------------------------------------------------

type readFileTool struct{ ws workspace.Workspace }

var _ tool.Tool = (*readFileTool)(nil)

func (t *readFileTool) Definition() message.ToolDefinition {
	return message.DefineSchema(
		ReadFileName,
		"Read a text file from the workspace and return one window of "+
			"it. Paths are relative to the workspace root. Returns JSON: "+
			"{file_path, content, offset, limit, total_lines, "+
			"is_truncated, next_offset}. A window carries as much as one "+
			"result can hold (about 20000 characters, or limit lines if "+
			"that is smaller); when lines remain, next_offset is where "+
			"the next read continues. Read a file whole when it fits, and "+
			"continue from next_offset otherwise: probing in small "+
			"ranges costs a round per probe.",
		message.ToolProperty("file_path", "string",
			"The file to read: relative to the workspace root, or an "+
				"absolute path under an allowed root (workspace, skill "+
				"roots, cache) in workspace mode (required)."),
		message.ToolProperty("offset", "integer",
			"1-based line to start from (default 1)."),
		message.ToolProperty("limit", "integer",
			fmt.Sprintf("Maximum number of lines to return (default %d, max %d).",
				defaultReadLimit, maxReadLimit)),
	).Required("file_path").DisallowAdditionalProperties().Build()
}

func (t *readFileTool) Metadata() tool.ToolMeta { return tool.ToolMeta{} }

// Execute implements tool.Tool. The tool result is a single text part;
// the tool has no multimodal output.
func (t *readFileTool) Execute(ctx context.Context, arguments string) (message.Content, error) {
	out, err := t.execute(ctx, arguments)
	if err != nil {
		return message.Content{}, err
	}
	return message.NewTextContent(out), nil
}

// execute renders the tool's text result.
func (t *readFileTool) execute(ctx context.Context, arguments string) (string, error) {
	var args struct {
		FilePath string `json:"file_path"`
		Offset   int    `json:"offset"`
		Limit    int    `json:"limit"`
	}
	if err := toolargs.Decode(ReadFileName, arguments, nil, &args); err != nil {
		return "", err
	}
	if err := validateFilePath(args.FilePath); err != nil {
		return "", err
	}
	data, oversized, err := readFileBounded(
		ctx, t.ws, args.FilePath, maxReadFileBytes)
	if err != nil {
		return "", err
	}
	if oversized {
		return "", errdefs.Validationf(
			"%s: %s exceeds the %d-byte limit; use grep or a narrower range",
			ReadFileName, args.FilePath, maxReadFileBytes)
	}
	// Binary image bytes are not text: returning them as a JSON string
	// would hand the model mojibake. Point at the tool that can show it.
	// The bytes name the type (see the filetype package), so a picture
	// saved under a text name still gets the pointer.
	kind := filetype.OfData(args.FilePath, data)
	if filetype.Family(kind.MediaType) == "image" {
		return fmt.Sprintf(
			"%s is an image (%s). Use view_image to look at it; read_file "+
				"returns text only.",
			args.FilePath, kind.MediaType,
		), nil
	}

	total := 0
	endsWithNL := false
	if len(data) > 0 {
		total = bytes.Count(data, []byte{'\n'})
		endsWithNL = data[len(data)-1] == '\n'
		if !endsWithNL {
			total++
		}
	}
	offset := args.Offset
	if offset < 1 {
		offset = 1
	}
	limit := args.Limit
	if limit <= 0 {
		limit = defaultReadLimit
	}
	if limit > maxReadLimit {
		limit = maxReadLimit
	}
	start := offset - 1
	if start > total {
		start = total
	}
	end := start + limit
	if end > total {
		end = total
	}
	content, reached := joinLineRange(data, start, end, ReadResultChars)
	if reached < total || (reached == total && total > 0 && endsWithNL) {
		content += "\n"
	}
	result := map[string]any{
		"file_path":    args.FilePath,
		"content":      content,
		"offset":       offset,
		"limit":        limit,
		"total_lines":  total,
		"is_truncated": reached < total,
	}
	// next_offset is where the next read continues: the window can stop
	// short of limit lines when the character budget fills first, so
	// offset+limit is not that answer.
	if reached < total {
		result["next_offset"] = reached + 1
	}
	payload, err := json.Marshal(result)
	if err != nil {
		return "", errdefs.Internalf("%s: encode result: %v", ReadFileName, err)
	}
	return string(payload), nil
}

// joinLineRange returns lines [start, end) of data as a single string
// without allocating the whole file as a line slice, stopping once the
// window has reached budget characters. The returned end is exclusive
// and comes up short of end when the budget stopped the window; the
// caller reports it as where the next read continues.
//
// One line always comes back, even when it alone exceeds the budget: a
// window that returned nothing could not be paged from. A line that
// long is usually a bundle or generated file, which the truncate
// middleware then excerpts with a pointer at the full output.
//
// Lines are split on '\n'; a trailing newline is not part of a line.
func joinLineRange(data []byte, start, end, budget int) (string, int) {
	if start >= end || len(data) == 0 {
		return "", start
	}
	var b strings.Builder
	line := 0
	idx := 0
	reached := start
	for line < end && idx < len(data) {
		nl := bytes.IndexByte(data[idx:], '\n')
		lineEnd := len(data)
		if nl >= 0 {
			lineEnd = idx + nl
		}
		if line >= start {
			if b.Len() > 0 && b.Len()+1+(lineEnd-idx) > budget {
				break
			}
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			b.Write(data[idx:lineEnd])
			reached = line + 1
		}
		if nl < 0 {
			break
		}
		idx = lineEnd + 1
		line++
	}
	return b.String(), reached
}

// ---------------------------------------------------------------------------
// write_file
// ---------------------------------------------------------------------------

type writeFileTool struct{ ws workspace.Workspace }

var _ tool.Tool = (*writeFileTool)(nil)

func (t *writeFileTool) Definition() message.ToolDefinition {
	return message.DefineSchema(
		WriteFileName,
		"Write content to a file in the workspace, creating parent "+
			"directories as needed. Overwrites existing content. "+
			"Paths are relative to the workspace root. Returns JSON: "+
			"{file_path, bytes}.",
		message.ToolProperty("file_path", "string",
			"The file to write, relative to the workspace root (required)."),
		message.ToolProperty("content", "string",
			"The full new file content (default empty)."),
	).Required("file_path").DisallowAdditionalProperties().Build()
}

func (t *writeFileTool) Metadata() tool.ToolMeta {
	return tool.ToolMeta{MutatesState: true}
}

// Execute implements tool.Tool. The tool result is a single text part;
// the tool has no multimodal output.
func (t *writeFileTool) Execute(ctx context.Context, arguments string) (message.Content, error) {
	out, err := t.execute(ctx, arguments)
	if err != nil {
		return message.Content{}, err
	}
	return message.NewTextContent(out), nil
}

// execute renders the tool's text result.
func (t *writeFileTool) execute(ctx context.Context, arguments string) (string, error) {
	var args struct {
		FilePath string `json:"file_path"`
		Content  string `json:"content"`
	}
	if err := toolargs.Decode(WriteFileName, arguments, nil, &args); err != nil {
		return "", err
	}
	if err := validateFilePath(args.FilePath); err != nil {
		return "", err
	}
	if err := t.ws.Write(ctx, args.FilePath, []byte(args.Content)); err != nil {
		return "", err
	}
	payload, err := json.Marshal(map[string]any{
		"file_path": args.FilePath,
		"bytes":     len(args.Content),
	})
	if err != nil {
		return "", errdefs.Internalf("%s: encode result: %v", WriteFileName, err)
	}
	return string(payload), nil
}

// ---------------------------------------------------------------------------
// list_dir
// ---------------------------------------------------------------------------

type listDirTool struct{ ws workspace.Workspace }

var _ tool.Tool = (*listDirTool)(nil)

func (t *listDirTool) Definition() message.ToolDefinition {
	return message.DefineSchema(
		ListDirName,
		"List files and directories under a workspace directory. The "+
			"path must name a directory; to inspect a file's contents "+
			"use read_file or grep. Returns "+
			"JSON: {path, entries:[{path, type, size}], truncated}. "+
			"Hidden entries and .git are skipped unless include_hidden "+
			"is true.",
		message.ToolProperty("path", "string",
			"Directory to list, relative to the workspace root (default \".\")."),
		message.ToolProperty("recursive", "boolean",
			"Recurse into subdirectories (default false)."),
		message.ToolProperty("include_hidden", "boolean",
			"Include hidden files and directories (default false)."),
		message.ToolProperty("max_depth", "integer",
			fmt.Sprintf("Recursion depth below path (default %d, max %d).",
				defaultListDepth, maxListDepth)),
	).DisallowAdditionalProperties().Build()
}

func (t *listDirTool) Metadata() tool.ToolMeta { return tool.ToolMeta{} }

type dirEntry struct {
	Path string `json:"path"`
	Type string `json:"type"`
	Size int64  `json:"size,omitempty"`
}

// Execute implements tool.Tool. The tool result is a single text part;
// the tool has no multimodal output.
func (t *listDirTool) Execute(ctx context.Context, arguments string) (message.Content, error) {
	out, err := t.execute(ctx, arguments)
	if err != nil {
		return message.Content{}, err
	}
	return message.NewTextContent(out), nil
}

// execute renders the tool's text result.
func (t *listDirTool) execute(ctx context.Context, arguments string) (string, error) {
	var args struct {
		Path          string `json:"path"`
		Recursive     bool   `json:"recursive"`
		IncludeHidden bool   `json:"include_hidden"`
		MaxDepth      int    `json:"max_depth"`
	}
	if err := toolargs.Decode(ListDirName, arguments, nil, &args); err != nil {
		return "", err
	}
	root := args.Path
	if root == "" {
		root = "."
	}
	if err := validateDirPath(root); err != nil {
		return "", err
	}
	maxDepth := args.MaxDepth
	if maxDepth == 0 {
		maxDepth = defaultListDepth
	}
	if maxDepth < 0 {
		maxDepth = 0
	}
	if maxDepth > maxListDepth {
		maxDepth = maxListDepth
	}
	if !args.Recursive {
		maxDepth = 0
	}

	info, err := t.ws.Stat(ctx, root)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errdefs.Validationf(
			"%s: path %q is a file, not a directory", ListDirName, root)
	}

	var entries []dirEntry
	count := 0
	truncated := false
	err = workspace.Walk(ctx, t.ws, root, func(p string, entry fs.DirEntry) error {
		name := entry.Name()
		if entry.IsDir() && name == alwaysSkipDir {
			return filepath.SkipDir
		}
		if !args.IncludeHidden && fshidden.Hidden(entry) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			depth := depthBelow(root, p)
			if !args.Recursive || depth > maxDepth {
				return filepath.SkipDir
			}
		}
		count++
		if count > maxWalkEntries {
			truncated = true
			return errStopWalk
		}
		de := dirEntry{Path: p, Type: "file"}
		if entry.IsDir() {
			de.Type = "dir"
		}
		if info, err := entry.Info(); err == nil {
			de.Size = info.Size()
		}
		entries = append(entries, de)
		return nil
	})
	if err != nil && !errors.Is(err, errStopWalk) {
		return "", err
	}
	payload, err := json.Marshal(map[string]any{
		"path":      root,
		"entries":   entries,
		"truncated": truncated,
	})
	if err != nil {
		return "", errdefs.Internalf("%s: encode result: %v", ListDirName, err)
	}
	return string(payload), nil
}

// ---------------------------------------------------------------------------
// grep
// ---------------------------------------------------------------------------

type grepTool struct{ ws workspace.Workspace }

var _ tool.Tool = (*grepTool)(nil)

func (t *grepTool) Definition() message.ToolDefinition {
	return message.DefineSchema(
		GrepName,
		"Search workspace files for lines matching a pattern. "+
			"path may name a directory (searched recursively) or a "+
			"single file. Returns JSON: {matches:[{path, line_number, "+
			"line}], truncated, skipped_large, binary_matches}. Hidden "+
			"entries and .git are skipped unless include_hidden is "+
			"true, or the entry is the file path itself; files larger "+
			"than 1 MiB are skipped, and a binary file that matches is "+
			"reported by path in binary_matches instead of by line.",
		message.ToolProperty("pattern", "string",
			"The search pattern: a regular expression unless "+
				"fixed_strings is true (required)."),
		message.ToolProperty("path", "string",
			"Directory to search, or one file to search, relative to "+
				"the workspace root (default \".\")."),
		message.ToolProperty("case_insensitive", "boolean",
			"Case-insensitive match (default false)."),
		message.ToolProperty("fixed_strings", "boolean",
			"Treat pattern as a literal string, not a regex (default false)."),
		message.ToolProperty("max_matches", "integer",
			fmt.Sprintf("Return at most this many matches (default %d, max %d).",
				defaultGrepMatches, maxGrepMatches)),
		message.ToolProperty("include_hidden", "boolean",
			"Include hidden files and directories (default false)."),
	).Required("pattern").DisallowAdditionalProperties().Build()
}

func (t *grepTool) Metadata() tool.ToolMeta { return tool.ToolMeta{} }

type grepMatch struct {
	Path       string `json:"path"`
	LineNumber int    `json:"line_number"`
	Line       string `json:"line"`
}

// Execute implements tool.Tool. The tool result is a single text part;
// the tool has no multimodal output.
func (t *grepTool) Execute(ctx context.Context, arguments string) (message.Content, error) {
	out, err := t.execute(ctx, arguments)
	if err != nil {
		return message.Content{}, err
	}
	return message.NewTextContent(out), nil
}

// execute renders the tool's text result.
func (t *grepTool) execute(ctx context.Context, arguments string) (string, error) {
	var args struct {
		Pattern         string `json:"pattern"`
		Path            string `json:"path"`
		CaseInsensitive bool   `json:"case_insensitive"`
		FixedStrings    bool   `json:"fixed_strings"`
		MaxMatches      int    `json:"max_matches"`
		IncludeHidden   bool   `json:"include_hidden"`
	}
	if err := toolargs.Decode(GrepName, arguments, nil, &args); err != nil {
		return "", err
	}
	if strings.TrimSpace(args.Pattern) == "" {
		return "", errdefs.Validationf("%s: pattern is required", GrepName)
	}
	root := args.Path
	if root == "" {
		root = "."
	}
	if err := validateDirPath(root); err != nil {
		return "", err
	}
	info, err := t.ws.Stat(ctx, root)
	if err != nil {
		return "", err
	}
	expr := args.Pattern
	if args.FixedStrings {
		expr = regexp.QuoteMeta(expr)
	}
	if args.CaseInsensitive {
		expr = "(?i)" + expr
	}
	re, err := regexp.Compile(expr)
	if err != nil {
		return "", errdefs.Validationf("%s: invalid pattern: %v", GrepName, err)
	}
	maxMatches := args.MaxMatches
	if maxMatches <= 0 {
		maxMatches = defaultGrepMatches
	}
	if maxMatches > maxGrepMatches {
		maxMatches = maxGrepMatches
	}

	scan := grepScan{re: re, maxMatches: maxMatches}
	// A path that names a file searches that file: "search inside this
	// one file" is a request the directory-only rule used to reject,
	// and the caller has already done the narrowing a tree walk exists
	// to do. Naming a file also outranks the hidden-entry rule, as it
	// does for read_file.
	if info.IsDir() {
		err = scan.walk(ctx, t.ws, root, args.IncludeHidden)
	} else {
		scan.file(ctx, t.ws, root, info.Size())
	}
	if err != nil && !errors.Is(err, errStopWalk) {
		return "", err
	}
	if len(scan.matches) > scan.maxMatches {
		scan.matches = scan.matches[:scan.maxMatches]
	}
	payload, err := json.Marshal(map[string]any{
		"matches":       scan.matches,
		"truncated":     scan.truncated,
		"skipped_large": scan.skippedLarge,
		// Paths of binary files (bundles, wasm, media) whose bytes
		// matched. Their lines are not returned: they are not text.
		"binary_matches": scan.binaryMatches,
	})
	if err != nil {
		return "", errdefs.Internalf("%s: encode result: %v", GrepName, err)
	}
	return string(payload), nil
}

// grepScan accumulates the hits of one grep call. Both shapes a call
// can take — a directory walked file by file, and the single file the
// caller named — go through it, so they report the same fields under
// the same caps.
type grepScan struct {
	re            *regexp.Regexp
	maxMatches    int
	matches       []grepMatch
	binaryMatches []string
	skippedLarge  int
	truncated     bool
}

// walk searches root's tree, skipping hidden entries and .git unless
// includeHidden is set, and stopping at maxGrepFiles.
func (s *grepScan) walk(
	ctx context.Context,
	ws workspace.Workspace,
	root string,
	includeHidden bool,
) error {
	scannedFiles := 0
	return workspace.Walk(ctx, ws, root, func(p string, entry fs.DirEntry) error {
		if entry.IsDir() {
			name := entry.Name()
			if name == alwaysSkipDir ||
				(!includeHidden && fshidden.Hidden(entry)) {
				return filepath.SkipDir
			}
			return nil
		}
		if !includeHidden && fshidden.Hidden(entry) {
			return nil
		}
		if len(s.matches) > s.maxMatches {
			// The list already holds one match over the cap, so the walk
			// is on a file whose hits cannot be reported: the result is
			// incomplete by construction.
			s.truncated = true
			return errStopWalk
		}
		scannedFiles++
		if scannedFiles > maxGrepFiles {
			s.truncated = true
			return errStopWalk
		}
		// The walk's own stat spares reading a file it cannot report on.
		size := int64(-1)
		if info, err := entry.Info(); err == nil {
			size = info.Size()
		}
		if !s.file(ctx, ws, p, size) {
			return errStopWalk
		}
		return nil
	})
}

// file searches one file and reports whether the walk may continue. An
// unreadable file is skipped: one entry the process cannot open must
// not fail a search of everything around it. size is the file's size
// when the caller has already stat'ed it, or -1 to let the read decide.
func (s *grepScan) file(
	ctx context.Context,
	ws workspace.Workspace,
	p string,
	size int64,
) bool {
	// Skip oversized files from the walk's own stat when it has one:
	// reading a MiB of a binary or a bundle only to discard it was
	// the bulk of what a grep allocated.
	if size > maxGrepFileBytes {
		s.skippedLarge++
		return true
	}
	data, oversized, err := readFileBounded(ctx, ws, p, maxGrepFileBytes)
	if err != nil {
		return true // unreadable file: skip
	}
	if oversized {
		s.skippedLarge++
		return true
	}
	// A NUL byte in the head of the file means binary content: a
	// matched line would be an unreadable slice of a bundle or an
	// asset. The fact that it matches is still signal — grep -l
	// reports exactly this — so search it whole and report the path
	// without scanning lines or building strings.
	if bytes.IndexByte(data[:min(len(data), binarySniffBytes)], 0) >= 0 {
		if s.re.Match(data) {
			s.binaryMatches = append(s.binaryMatches, p)
		}
		return true
	}
	capped := false
	scanLines(data, func(lineNumber int, line []byte) bool {
		if !s.re.Match(line) {
			return true
		}
		s.matches = append(s.matches, grepMatch{
			Path:       p,
			LineNumber: lineNumber,
			Line:       string(line),
		})
		// One match over the cap is the smallest amount of evidence
		// that the cap is hiding hits, and it is what separates a
		// complete answer from an incomplete one: a scan that stops
		// as soon as the list is full cannot tell whether the last
		// hit it kept was also the last hit in the tree, so it
		// reported truncation for exactly-full results too.
		capped = len(s.matches) > s.maxMatches
		return !capped
	})
	if capped {
		// The scan stopped at a dropped hit rather than at the end
		// of its input.
		s.truncated = true
		return false
	}
	return true
}

// scanLines calls emit once per line of data with the line's 1-based
// number, splitting on "\n" with the same trailing-empty-line
// semantics as strings.Split(string(data), "\n"). Lines are sliced out
// of data rather than copied, so scanning a file that does not match
// allocates nothing.
func scanLines(data []byte, emit func(lineNumber int, line []byte) bool) {
	lineNumber := 0
	rest := data
	for {
		lineNumber++
		i := bytes.IndexByte(rest, '\n')
		line := rest
		if i < 0 {
			rest = nil
		} else {
			line, rest = rest[:i], rest[i+1:]
		}
		if !emit(lineNumber, line) {
			return
		}
		if i < 0 {
			return
		}
	}
}

// ---------------------------------------------------------------------------
// glob
// ---------------------------------------------------------------------------

type globTool struct{ ws workspace.Workspace }

var _ tool.Tool = (*globTool)(nil)

func (t *globTool) Definition() message.ToolDefinition {
	return message.DefineSchema(
		GlobName,
		"Find files in the workspace matching a glob pattern. "+
			"Supports * (one segment), ** (any number of segments), "+
			"? and [class]. Returns JSON: {matches:[path], truncated}, "+
			"with workspace-relative paths.",
		message.ToolProperty("pattern", "string",
			"The glob pattern, applied under path (required), e.g. "+
				"\"**/*_test.go\" or \"internal/**/tool.go\"."),
		message.ToolProperty("path", "string",
			"Base directory, relative to the workspace root (default \".\")."),
	).Required("pattern").DisallowAdditionalProperties().Build()
}

func (t *globTool) Metadata() tool.ToolMeta { return tool.ToolMeta{} }

// Execute implements tool.Tool. The tool result is a single text part;
// the tool has no multimodal output.
func (t *globTool) Execute(ctx context.Context, arguments string) (message.Content, error) {
	out, err := t.execute(ctx, arguments)
	if err != nil {
		return message.Content{}, err
	}
	return message.NewTextContent(out), nil
}

// execute renders the tool's text result.
func (t *globTool) execute(ctx context.Context, arguments string) (string, error) {
	var args struct {
		Pattern string `json:"pattern"`
		Path    string `json:"path"`
	}
	if err := toolargs.Decode(GlobName, arguments, nil, &args); err != nil {
		return "", err
	}
	if strings.TrimSpace(args.Pattern) == "" {
		return "", errdefs.Validationf("%s: pattern is required", GlobName)
	}
	root := args.Path
	if root == "" {
		root = "."
	}
	if err := validateDirPath(root); err != nil {
		return "", err
	}
	if err := validatePattern(args.Pattern); err != nil {
		return "", err
	}
	// The pattern is relative to path, so it is applied under it:
	// workspace.Glob matches whole workspace paths, and handing it
	// "*.go" unchanged while filtering the results by the "internal"
	// prefix meant {pattern:"*.go", path:"internal"} could only ever
	// find files whose root-relative path literally starts with
	// "internal" and matches "*.go" — that is, nothing.
	pattern := args.Pattern
	if root != "." {
		pattern = path.Join(root, pattern)
	}
	matches, err := workspace.Glob(ctx, t.ws, pattern)
	if err != nil {
		return "", err
	}
	truncated := false
	if len(matches) > maxGlobMatches {
		matches = matches[:maxGlobMatches]
		truncated = true
	}
	payload, err := json.Marshal(map[string]any{
		// Paths are workspace-relative, the namespace every other file
		// tool reports in.
		"matches":   matches,
		"truncated": truncated,
	})
	if err != nil {
		return "", errdefs.Internalf("%s: encode result: %v", GlobName, err)
	}
	return string(payload), nil
}

// ---------------------------------------------------------------------------
// shared walking
// ---------------------------------------------------------------------------

// errStopWalk stops a walk early without marking it truncated.
var errStopWalk = errors.New("files: stop walk")

// depthBelow returns p's depth below root (0 for direct children).
//
// The walk hands back workspace paths, which are slash-separated on every
// platform, so depth counts "/" and never the host separator: with
// filepath.Separator every Windows path would read as depth 0.
func depthBelow(root, p string) int {
	if root == "." {
		return strings.Count(p, "/")
	}
	rel := strings.TrimPrefix(p, strings.TrimSuffix(root, "/")+"/")
	return strings.Count(rel, "/")
}

// validateFilePath rejects "..", ".", and empty paths for file
// operations. Absolute paths are allowed through to the workspace,
// which enforces the per-mode policy (confined root, readonly skill
// roots, or YOLO host paths).
func validateFilePath(p string) error {
	if p == "" {
		return errdefs.Validationf("files: path is required")
	}
	if err := validatePath(p); err != nil {
		return err
	}
	if p == "." {
		return errdefs.Validationf("files: path must name a file, not a directory")
	}
	return nil
}

// validateDirPath allows "." as the workspace root.
func validateDirPath(p string) error {
	if p == "" {
		return errdefs.Validationf("files: path is required")
	}
	return validatePath(p)
}

func validatePath(p string) error {
	if strings.Contains(p, "\\") {
		return errdefs.Validationf("files: backslash in path %q rejected; use forward slashes", p)
	}
	// Cleanliness is judged on the workspace namespace, which is
	// slash-separated on every platform: filepath.Clean rewrites
	// "src/main.go" into backslashes on Windows, which would reject
	// every nested path the tools exist to accept.
	if path.Clean(p) != p || p == ".." || strings.HasPrefix(p, "../") {
		return errdefs.Validationf("files: path %q must be clean and relative", p)
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return errdefs.Validationf("files: path traversal %q rejected", p)
		}
	}
	return nil
}

func validatePattern(p string) error {
	for _, seg := range strings.Split(strings.TrimPrefix(p, "./"), "/") {
		if seg == ".." {
			return errdefs.Validationf("files: pattern traversal %q rejected", p)
		}
	}
	return nil
}

// readFileBounded reads at most maxBytes+1 through the workspace's
// LimitedReader when available, falling back to the plain Read API for
// workspaces that do not implement bounded reads. oversized reports
// whether the file is larger than maxBytes.
func readFileBounded(
	ctx context.Context,
	ws workspace.Workspace,
	path string,
	maxBytes int,
) (data []byte, oversized bool, err error) {
	if lr, ok := ws.(workspace.LimitedReader); ok {
		data, err = lr.ReadLimited(ctx, path, int64(maxBytes)+1)
		if err != nil {
			return nil, false, err
		}
		return data, len(data) > maxBytes, nil
	}
	data, err = ws.Read(ctx, path)
	if err != nil {
		return nil, false, err
	}
	return data, len(data) > maxBytes, nil
}
