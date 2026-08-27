// Package skills discovers, indexes and serves Agent Skills
// (SKILL.md directories, agentskills.io). The package owns the
// shared opencraft.skills resource: discovery happens once per
// service lifetime, the result is cached, and both the worldstate
// prepare hook (per-turn dynamic injection) and the skill_search /
// skill_read tools consume the same instance.
package skills

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
	"unicode/utf8"

	"github.com/GizClaw/opencraft/internal/utils/search"

	"github.com/GizClaw/flowcraft/core/workspace"
	"sigs.k8s.io/yaml"
)

// Limits mirror agentskills.io plus codex-rs parser behaviour.
const (
	maxNameLen        = 64
	maxDescriptionLen = 1024
	defaultTopN       = 5
)

// SkillMetadata is one skill parsed from a SKILL.md file.
type SkillMetadata struct {
	Name             string `json:"name"`
	Description      string `json:"description"`
	ShortDescription string `json:"short_description,omitempty"` // metadata.short-description (non-standard extension)
	Path             string `json:"path"`                        // absolute path of SKILL.md
	// Scope classifies the source root for trust display:
	// "repo" | "user" | "builtin".
	Scope string `json:"scope,omitempty"`
	// Depth orders scope priority for duplicate names: repo levels run
	// root -> cwd (cwd highest), then user dirs, then extra roots. A
	// higher Depth is closer to the user and wins on $mention.
	Depth int `json:"-"`
}

// SkillError is a non-fatal discovery diagnostic (parse failure,
// validation fallback, ...).
type SkillError struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

// SkillLoadOutcome is the result of one discovery pass.
type SkillLoadOutcome struct {
	Skills []SkillMetadata
	Errors []SkillError
	Index  *search.Index
	Roots  []string
	// ScanRoots lists every candidate root that was walked (whether or
	// not it contained a skill). Discovery and reads use it to keep
	// symlinks from escaping the configured skill roots.
	ScanRoots []string
}

// Options configures a Service.
type Options struct {
	WorkBase   string
	UserDir    string
	Workspace  workspace.Workspace // optional; in-root reads go through it
	Enabled    bool
	TopN       int
	MinScore   float64
	ExtraRoots []string // additional absolute skill roots
	// Disabled lists skill names or absolute SKILL.md paths excluded
	// from discovery ([[skills.config]] enabled=false semantics).
	Disabled []string
}

// Service is the shared skills registry: one discovery pass, one BM25
// index, plus read access to SKILL.md bodies. The registry is an
// immutable snapshot under an atomic pointer, so Reload (used by the
// skill_install tool) can swap in a freshly discovered registry
// without locking the hot read paths (worldstate per-turn ranking,
// tools).
type Service struct {
	opts     Options
	snapshot atomic.Pointer[snapshot]
}

// snapshot is one immutable discovery result.
type snapshot struct {
	outcome   SkillLoadOutcome
	byPath    map[string]SkillMetadata
	roots     []string
	scanRoots []string
}

// NewService discovers skills and builds the shared index. A disabled
// service stays empty (worldstate and tools see no skills).
func NewService(opts Options) *Service {
	if opts.TopN <= 0 {
		opts.TopN = defaultTopN
	}
	s := &Service{opts: opts}
	s.reload()
	return s
}

// Reload re-runs discovery and swaps in a fresh snapshot. Installed
// skills become visible immediately (no restart needed).
func (s *Service) Reload() { s.reload() }

func (s *Service) reload() {
	if !s.opts.Enabled {
		s.snapshot.Store(&snapshot{})
		return
	}
	outcome := Discover(s.opts.WorkBase, s.opts.UserDir, s.opts.Workspace, s.opts.ExtraRoots)
	outcome.Skills = filterDisabled(outcome.Skills, s.opts.Disabled)
	outcome.Skills = append(outcome.Skills,
		filterDisabled(builtinSkills(), s.opts.Disabled)...)
	byPath := make(map[string]SkillMetadata, len(outcome.Skills))
	docs := make([]search.Doc, 0, len(outcome.Skills))
	for _, s := range outcome.Skills {
		byPath[s.Path] = s
		docs = append(docs, search.Doc{
			ID:   s.Path,
			Name: s.Name,
			Text: s.Description,
		})
	}
	outcome.Index = search.NewIndex(docs)
	s.snapshot.Store(&snapshot{
		outcome:   outcome,
		byPath:    byPath,
		roots:     outcome.Roots,
		scanRoots: cleanedRoots(outcome.ScanRoots),
	})
}

// filterDisabled drops skills whose name or path matches the disabled
// list (exact match on name, prefix-free absolute path match).
func filterDisabled(skills []SkillMetadata, disabled []string) []SkillMetadata {
	if len(disabled) == 0 {
		return skills
	}
	names := map[string]bool{}
	paths := map[string]bool{}
	for _, d := range disabled {
		d = strings.TrimSpace(d)
		if d == "" {
			continue
		}
		if strings.HasPrefix(d, "/") {
			paths[filepath.Clean(d)] = true
		} else {
			names[d] = true
		}
	}
	if len(names) == 0 && len(paths) == 0 {
		return skills
	}
	out := skills[:0]
	for _, sk := range skills {
		if names[sk.Name] || paths[filepath.Clean(sk.Path)] {
			continue
		}
		out = append(out, sk)
	}
	return out
}

// Enabled reports whether discovery is on.
func (s *Service) Enabled() bool { return s.opts.Enabled }

// TopN returns the configured ranked-list size.
func (s *Service) TopN() int { return s.opts.TopN }

// MinScore returns the configured BM25 threshold.
func (s *Service) MinScore() float64 { return s.opts.MinScore }

// List returns all discovered skills sorted by name, then path.
func (s *Service) List() []SkillMetadata {
	snap := s.snapshot.Load()
	out := append([]SkillMetadata(nil), snap.outcome.Skills...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Path < out[j].Path
	})
	return out
}

// Errors returns the non-fatal diagnostics from the last discovery.
func (s *Service) Errors() []SkillError {
	snap := s.snapshot.Load()
	return append([]SkillError(nil), snap.outcome.Errors...)
}

// Roots returns the scan roots that contained at least one skill.
func (s *Service) Roots() []string {
	snap := s.snapshot.Load()
	return append([]string(nil), snap.roots...)
}

// ByName resolves a skill by name. Duplicate names are resolved to the
// nearest scope (cwd layer > ancestors > repo root > user dirs >
// extra roots), matching D3.
func (s *Service) ByName(name string) (SkillMetadata, bool) {
	snap := s.snapshot.Load()
	var best SkillMetadata
	found := false
	for _, sk := range snap.outcome.Skills {
		if sk.Name != name {
			continue
		}
		if !found || sk.Depth > best.Depth {
			best = sk
			found = true
		}
	}
	return best, found
}

// mentionRe matches $name mentions that stand alone (start of text or
// preceded by a non-word character), so "$50" inside "$500" or
// "a$skill" mid-word is not treated as a mention.
var mentionRe = regexp.MustCompile(`(?:^|[^a-z0-9_])[$]([a-z0-9]+(?:-[a-z0-9]+)*)`)

// Mentioned extracts explicit $name mentions from text and resolves
// them to skills (nearest scope wins per name, in mention order).
func (s *Service) Mentioned(text string) []SkillMetadata {
	var out []SkillMetadata
	seen := map[string]bool{}
	for _, m := range mentionRe.FindAllStringSubmatch(text, -1) {
		sk, ok := s.ByName(m[1])
		if !ok || seen[sk.Path] {
			continue
		}
		seen[sk.Path] = true
		out = append(out, sk)
	}
	return out
}

// Rank returns the topN skills for query scoring at least minScore,
// sorted by BM25 score descending. A minScore <= 0 accepts any match
// (the index only returns documents with score > 0).
func (s *Service) Rank(query string, topN int, minScore float64) []SkillMetadata {
	scored := s.RankScored(query, topN, minScore)
	out := make([]SkillMetadata, 0, len(scored))
	for _, sc := range scored {
		out = append(out, sc.Skill)
	}
	return out
}

// ScoredSkill couples one ranked skill with its BM25 score, exposed
// for observability (skill_search output, telemetry, threshold tuning).
type ScoredSkill struct {
	Skill SkillMetadata
	Score float64
}

// RankScored is Rank with scores attached.
func (s *Service) RankScored(query string, topN int, minScore float64) []ScoredSkill {
	snap := s.snapshot.Load()
	if snap.outcome.Index == nil || strings.TrimSpace(query) == "" {
		return nil
	}
	limit := topN
	if limit <= 0 {
		limit = s.opts.TopN
	}
	results := snap.outcome.Index.Search(query, limit)
	out := make([]ScoredSkill, 0, len(results))
	for _, r := range results {
		if minScore > 0 && r.Score < minScore {
			continue
		}
		if sk, ok := snap.byPath[r.ID]; ok {
			out = append(out, ScoredSkill{Skill: sk, Score: r.Score})
		}
	}
	return out
}

// ReadFull returns a skill's metadata plus its full SKILL.md body
// (activation path and skill_read share this).
func (s *Service) ReadFull(name string) (SkillMetadata, string, error) {
	sk, ok := s.ByName(name)
	if !ok {
		return SkillMetadata{}, "", fmt.Errorf("skills: %q not found", name)
	}
	if strings.HasPrefix(sk.Path, "builtin://") {
		body, err := readBuiltin(sk.Path)
		if err != nil {
			return SkillMetadata{}, "", fmt.Errorf("skills: read %s: %w", sk.Path, err)
		}
		return sk, strings.TrimSpace(body), nil
	}
	snap := s.snapshot.Load()
	resolved, err := filepath.EvalSymlinks(sk.Path)
	if err != nil {
		return SkillMetadata{}, "", fmt.Errorf("skills: resolve %s: %w", sk.Path, err)
	}
	if !insideAnyRoot(resolved, snap.scanRoots) {
		return SkillMetadata{}, "", fmt.Errorf(
			"skills: %q resolves outside the configured skill roots", sk.Path)
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		return SkillMetadata{}, "", fmt.Errorf("skills: read %s: %w", sk.Path, err)
	}
	_, body, err := splitFrontmatter(data)
	if err != nil {
		return SkillMetadata{}, "", fmt.Errorf("skills: %s: %w", sk.Path, err)
	}
	return sk, strings.TrimSpace(string(body)), nil
}

// RenderSection renders the per-turn "## Skills" metadata list. Bodies
// are never inlined; the model opens them via the always-exposed skill
// tools: skill_search to find one and skill_read to load it.
func RenderSection(skills []SkillMetadata) string {
	if len(skills) == 0 {
		return ""
	}
	return renderSkillsSection(skills)
}

// Discover scans repo-level .agents/skills from cwd up to the repo
// root (each level), then ~/.agents/skills, <userDir>/skills and any
// extra roots, collecting every SKILL.md. Parse failures are recorded
// in Errors, never fatal. Duplicate paths are de-duplicated.
func Discover(
	workBase, userDir string,
	ws workspace.Workspace,
	extraRoots []string,
) SkillLoadOutcome {
	scans := buildScanRoots(workBase, userDir, extraRoots)
	c := collector{
		workBase:  workBase,
		ws:        ws,
		seen:      map[string]bool{},
		foundRoot: map[string]bool{},
		roots:     cleanedRoots(scanRootPaths(scans)),
	}
	for _, scan := range scans {
		c.scanRoot(scan.path, scan.depth, scan.scope)
	}
	out := SkillLoadOutcome{
		Skills:    c.skills,
		Errors:    c.errors,
		ScanRoots: scanRootPaths(scans),
	}
	sort.Slice(out.Skills, func(i, j int) bool {
		if out.Skills[i].Name != out.Skills[j].Name {
			return out.Skills[i].Name < out.Skills[j].Name
		}
		return out.Skills[i].Path < out.Skills[j].Path
	})
	for _, scan := range scans {
		root := scan.path
		if c.foundRoot[filepath.Clean(root)] {
			out.Roots = append(out.Roots, root)
		}
	}
	return out
}

// scanRoot is one configured skill scan root with its scope priority.
type scanRoot struct {
	path  string
	depth int
	scope string
}

func buildScanRoots(workBase, userDir string, extraRoots []string) []scanRoot {
	// Scope priority for duplicate names (D3): repo levels rank
	// root -> cwd (cwd highest), then user dirs, then extra roots.
	// Depth is that priority; ByName picks the highest.
	var scans []scanRoot
	for i, dir := range repoLevels(workBase) {
		scans = append(scans, scanRoot{
			path:  filepath.Join(dir, ".agents", "skills"),
			depth: i + 1,
			scope: "repo",
		})
	}
	if home, err := os.UserHomeDir(); err == nil {
		scans = append(scans, scanRoot{
			path:  filepath.Join(home, ".agents", "skills"),
			scope: "user",
		})
	}
	if userDir != "" {
		scans = append(scans, scanRoot{
			path:  filepath.Join(userDir, "skills"),
			scope: "user",
		})
	}
	for _, root := range extraRoots {
		scans = append(scans, scanRoot{path: root, scope: "user"})
	}
	return scans
}

func scanRootPaths(scans []scanRoot) []string {
	out := make([]string, 0, len(scans))
	for _, s := range scans {
		out = append(out, s.path)
	}
	return out
}

// cleanedRoots returns the absolute, symlink-resolved form of each
// root, so containment checks compare canonical paths (macOS /var is a
// symlink to /private/var, for example).
func cleanedRoots(roots []string) []string {
	out := make([]string, 0, len(roots))
	for _, r := range roots {
		abs, err := filepath.Abs(r)
		if err != nil {
			continue
		}
		clean := filepath.Clean(abs)
		if resolved, err := filepath.EvalSymlinks(clean); err == nil {
			clean = resolved
		}
		out = append(out, clean)
	}
	return out
}

// insideAnyRoot reports whether resolved stays inside one of roots,
// both lexically and through symlinks (callers resolve first).
func insideAnyRoot(resolved string, roots []string) bool {
	clean := filepath.Clean(resolved)
	for _, root := range roots {
		if clean == root ||
			strings.HasPrefix(clean, root+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// repoLevels returns the directories from the repo root down to
// workBase (root first). Falls back to workBase when no .git marker
// is found above it.
func repoLevels(workBase string) []string {
	root := workBase
	for {
		if _, err := os.Stat(filepath.Join(root, ".git")); err == nil {
			break
		}
		parent := filepath.Dir(root)
		if parent == root {
			root = workBase
			break
		}
		root = parent
	}
	var levels []string
	for dir := workBase; ; {
		levels = append(levels, dir)
		if dir == root {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	for i, j := 0, len(levels)-1; i < j; i, j = i+1, j-1 {
		levels[i], levels[j] = levels[j], levels[i]
	}
	return levels
}

type collector struct {
	workBase  string
	ws        workspace.Workspace
	seen      map[string]bool
	foundRoot map[string]bool
	roots     []string // cleaned scan roots for symlink containment
	skills    []SkillMetadata
	errors    []SkillError
}

// scanRoot BFS-walks one skill root, following symlinks (D2) and
// skipping hidden entries, collecting every SKILL.md.
func (c *collector) scanRoot(root string, depth int, scope string) {
	queue := []string{root}
	for len(queue) > 0 {
		dir := queue[0]
		queue = queue[1:]
		entries, err := c.listDir(dir)
		if err != nil {
			if !os.IsNotExist(err) {
				c.errors = append(c.errors, SkillError{Path: dir, Message: err.Error()})
			}
			continue
		}
		for _, entry := range entries {
			name := entry.Name()
			if strings.HasPrefix(name, ".") {
				continue
			}
			full := filepath.Join(dir, name)
			isLink := entry.Type()&fs.ModeSymlink != 0
			var resolved string
			if isLink {
				// Resolve once and verify the target stays inside the
				// configured skill roots: a repo-supplied symlink must
				// never redirect discovery (or skill_read) outside them.
				target, err := filepath.EvalSymlinks(full)
				if err != nil {
					c.errors = append(c.errors, SkillError{
						Path: full, Message: "resolve symlink: " + err.Error(),
					})
					continue
				}
				if !insideAnyRoot(target, c.roots) {
					c.errors = append(c.errors, SkillError{
						Path:    full,
						Message: "symlink escapes the configured skill roots",
					})
					continue
				}
				resolved = target
			} else {
				resolved = full
			}
			info, err := os.Stat(resolved)
			if err != nil {
				continue
			}
			if info.IsDir() {
				queue = append(queue, full)
				continue
			}
			if name != "SKILL.md" {
				continue
			}
			clean := filepath.Clean(full)
			if c.seen[clean] {
				continue
			}
			res, err := ParseFile(resolved)
			if err != nil {
				c.errors = append(c.errors, SkillError{Path: full, Message: err.Error()})
				continue
			}
			for _, w := range res.Warnings {
				c.errors = append(c.errors, SkillError{Path: full, Message: w})
			}
			sk := res.Metadata
			sk.Depth = depth
			sk.Scope = scope
			c.seen[clean] = true
			c.skills = append(c.skills, sk)
			c.foundRoot[filepath.Clean(root)] = true
		}
	}
}

// ParseResult carries the parsed metadata plus non-fatal diagnostics.
type ParseResult struct {
	Metadata SkillMetadata
	Warnings []string
}

// ParseFile parses one SKILL.md: YAML frontmatter, validation and
// sanitization. A missing or invalid name falls back to the parent
// directory name (D4) with a warning; only malformed files fail.
func ParseFile(path string) (ParseResult, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return ParseResult{}, err
	}
	return parseBytes(path, data)
}

// parseBytes parses SKILL.md content: YAML frontmatter, validation and
// sanitization. A missing or invalid name falls back to the parent
// directory name (D4) with a warning; only malformed files fail.
func parseBytes(path string, data []byte) (ParseResult, error) {
	fm, body, err := splitFrontmatter(data)
	if err != nil {
		return ParseResult{}, err
	}
	if strings.TrimSpace(string(body)) == "" {
		return ParseResult{}, fmt.Errorf("SKILL.md body is empty")
	}
	var f struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
		Metadata    struct {
			ShortDescription string `yaml:"short-description"`
		} `yaml:"metadata"`
	}
	if err := yaml.Unmarshal(fm, &f); err != nil {
		return ParseResult{}, fmt.Errorf("parse frontmatter: %w", err)
	}
	f.Name = sanitizeSingleLine(f.Name)
	f.Description = sanitizeSingleLine(f.Description)
	fallback := slugify(filepath.Base(filepath.Dir(path)))
	var warnings []string
	if !validName(f.Name) {
		note := "name missing or invalid"
		if f.Name != "" {
			note = fmt.Sprintf("name %q invalid", f.Name)
		}
		f.Name = fallback
		if f.Name == "" {
			return ParseResult{}, fmt.Errorf("%s and directory name is not usable as a fallback", note)
		}
		warnings = append(warnings, fmt.Sprintf("%s; fell back to directory name %q", note, fallback))
	} else if f.Name != fallback && fallback != "" {
		// Standard requires name == directory name; third-party skills
		// commonly violate it, so warn instead of rejecting (D4).
		warnings = append(warnings,
			fmt.Sprintf("name %q does not match directory name %q (accepted)", f.Name, fallback))
	}
	if f.Description == "" {
		return ParseResult{}, fmt.Errorf("description is required")
	}
	if utf8.RuneCountInString(f.Name) > maxNameLen {
		return ParseResult{}, fmt.Errorf("name longer than %d characters", maxNameLen)
	}
	if utf8.RuneCountInString(f.Description) > maxDescriptionLen {
		f.Description = truncateUTF8(f.Description, maxDescriptionLen)
	}
	return ParseResult{
		Metadata: SkillMetadata{
			Name:             f.Name,
			Description:      f.Description,
			ShortDescription: sanitizeSingleLine(f.Metadata.ShortDescription),
			Path:             filepath.Clean(path),
		},
		Warnings: warnings,
	}, nil
}

// splitFrontmatter extracts the YAML frontmatter (between the first
// two --- delimiters) and the Markdown body.
func splitFrontmatter(data []byte) (front []byte, body []byte, err error) {
	s := string(data)
	if !strings.HasPrefix(s, "---") {
		return nil, nil, fmt.Errorf("missing YAML frontmatter (expected --- ... ---)")
	}
	rest := s[3:]
	if strings.HasPrefix(rest, "\n") {
		rest = rest[1:]
	} else if strings.HasPrefix(rest, "\r\n") {
		rest = rest[2:]
	}
	idx := strings.Index(rest, "\n---")
	if idx < 0 {
		return nil, nil, fmt.Errorf("unterminated YAML frontmatter")
	}
	front = []byte(rest[:idx])
	body = []byte(rest[idx+4:])
	return front, body, nil
}

var nameRe = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

func validName(name string) bool {
	return name != "" && utf8.RuneCountInString(name) <= maxNameLen && nameRe.MatchString(name)
}

// slugify converts an arbitrary directory name into a valid skill
// name (lowercase, hyphens), the fallback codex-rs applies for
// third-party skills without a usable name field.
func slugify(s string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if b.Len() > 0 && !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if utf8.RuneCountInString(out) > maxNameLen {
		out = string([]rune(out)[:maxNameLen])
	}
	return out
}

// sanitizeSingleLine folds all whitespace runs into single spaces,
// mirroring codex-rs sanitize_single_line.
func sanitizeSingleLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func truncateUTF8(s string, max int) string {
	if len(s) <= max {
		return s
	}
	end := max
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	return s[:end]
}

func (c *collector) listDir(dir string) ([]fs.DirEntry, error) {
	if c.ws != nil {
		if rel, ok := withinRoot(dir, c.workBase); ok {
			entries, err := c.ws.List(context.Background(), rel)
			if err == nil {
				return entries, nil
			}
			if err == workspace.ErrNotFound || os.IsNotExist(err) {
				return nil, os.ErrNotExist
			}
			return nil, err
		}
	}
	return os.ReadDir(dir)
}

// withinRoot returns the workspace-relative path when dir is inside
// root, else ok=false.
func withinRoot(dir, root string) (string, bool) {
	rel, err := filepath.Rel(root, dir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	if rel == "." {
		return ".", true
	}
	return rel, true
}
