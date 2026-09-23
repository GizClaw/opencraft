// Package skills discovers, indexes and serves Agent Skills
// (SKILL.md directories, agentskills.io). The package owns the
// shared opencraft.skills resource: discovery runs when the service
// is constructed and again on every Reload, and both the worldstate
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
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/GizClaw/flowcraft/core/telemetry"
	otellog "go.opentelemetry.io/otel/log"

	skillusage "github.com/GizClaw/opencraft/internal/capabilities/skills/usage"
	"github.com/GizClaw/opencraft/internal/foundation/config"
	"github.com/GizClaw/opencraft/internal/foundation/utils/pathsafe"
	"github.com/GizClaw/opencraft/internal/foundation/utils/search"

	"sigs.k8s.io/yaml"
)

// Limits mirror agentskills.io parser behaviour.
const (
	maxNameLen        = 64
	maxDescriptionLen = 1024
	// maxSkillFileBytes caps one SKILL.md document (frontmatter + body).
	// Discovery, activation, and skill_read all refuse larger files so a
	// third-party skill cannot inject an unbounded document into the
	// model context or memory.
	maxSkillFileBytes = 256 << 10 // 256 KiB
	defaultTopN       = 5
)

// SkillMetadata is one skill parsed from a SKILL.md file.
type SkillMetadata struct {
	Name             string `json:"name"`
	Description      string `json:"description"`
	ShortDescription string `json:"short_description,omitempty"` // metadata.short-description (non-standard extension)
	Path             string `json:"path"`                        // absolute path of SKILL.md
	// Scope classifies the source root for trust display:
	// "user" (personal and plugin roots) | "builtin".
	Scope string `json:"scope,omitempty"`
	// Depth orders duplicate-name priority: every scanned root shares
	// depth 0 (a duplicate across scanned roots keeps the first path
	// in the sorted skill list) and builtins run at -1, so any user
	// skill beats its same-named builtin twin on $mention.
	Depth int `json:"-"`
}

// SkillError is a non-fatal discovery diagnostic (parse failure,
// validation fallback, ...).
type SkillError struct {
	Path    string `json:"path"`
	Message string `json:"message"`
	// Warning marks a tolerated shape issue (a third-party name that
	// differs from its directory, for example) that discovery accepted.
	// Callers log these at a lower severity than real errors.
	Warning bool `json:"warning,omitempty"`
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
	UserDir  string
	Enabled  bool
	TopN     int
	MinScore float64
	// ExtraRoots are additional absolute skill roots (plugin-provided
	// skills land here).
	ExtraRoots []string
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
	ctx      context.Context
	opts     Options
	snapshot atomic.Pointer[snapshot]

	// Lifecycle state: the user.db usage/decision store and the
	// curator built over it. Both are optional; a runtime without a
	// user database leaves them nil and every consumer degrades.
	lifecycle skillusage.Lifecycle
	curator   *Curator
	// recording mirrors the deploy's skill lifecycle switch. The store
	// stays wired when the switch is off (the page and the curator read
	// it), but nothing is recorded: "enabled turns usage recording on"
	// is what the setting documents, and a runtime that only reads is
	// exactly what it promises.
	recording   bool
	retiredMu   sync.Mutex
	retiredAt   time.Time
	retiredSet  map[string]bool
	retiredDone bool
}

// retiredCacheTTL bounds how long the retired-name set is reused. The
// skills page writes decisions straight to the store (it does not go
// through this service), so the registry re-reads them within this
// window instead of per call: the filter sits on the per-turn ranking
// path.
const retiredCacheTTL = 5 * time.Second

// snapshot is one immutable discovery result.
type snapshot struct {
	outcome   SkillLoadOutcome
	byPath    map[string]SkillMetadata
	roots     []string
	scanRoots []string
}

// NewService discovers skills and builds the shared index. A disabled
// service stays empty (worldstate and tools see no skills).
func NewService(ctx context.Context, opts Options) *Service {
	if opts.TopN <= 0 {
		opts.TopN = defaultTopN
	}
	s := &Service{ctx: ctx, opts: opts}
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
	// A retire/restore goes through this service and reloads the
	// registry right after; the retired-name cache must not keep serving
	// the set it built before that decision for the rest of its TTL.
	s.invalidateRetired()
	outcome := Discover(
		s.ctx,
		s.opts.UserDir,
		s.opts.ExtraRoots,
	)
	outcome.Skills = filterDisabled(outcome.Skills, s.opts.Disabled)
	outcome.Skills = append(outcome.Skills,
		filterDisabled(builtinSkills(), s.opts.Disabled)...)
	// A builtin skill loses to any same-named user skill (higher
	// Depth), matching ByName's $mention resolution. Without this a
	// same-named user skill and its builtin twin both show up in List
	// and can both rank into top-N. Same-name entries across user
	// roots are intentionally kept: ByName resolves by depth while
	// List and ranking may show each path.
	outcome.Skills = dropShadowedBuiltins(outcome.Skills)
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

// dropShadowedBuiltins removes a builtin entry whenever a non-builtin
// skill with the same name is discovered, so the builtin cannot crowd
// a user skill out of List or top-N ranking. Non-builtin duplicates
// across roots are left untouched.
func dropShadowedBuiltins(skills []SkillMetadata) []SkillMetadata {
	hasUserCopy := make(map[string]bool, len(skills))
	for _, sk := range skills {
		if sk.Scope != "builtin" {
			hasUserCopy[sk.Name] = true
		}
	}
	out := skills[:0]
	for _, sk := range skills {
		if sk.Scope == "builtin" && hasUserCopy[sk.Name] {
			continue
		}
		out = append(out, sk)
	}
	return out
}

// Enabled reports whether discovery is on.
func (s *Service) Enabled() bool { return s.opts.Enabled }

// SetLifecycle wires the user.db skill lifecycle store and builds the
// curator over it. A nil store (or an empty one) leaves the service
// stateless: nothing is recorded and no skill is ever filtered as
// retired. The settings switch only gates recording (see RecordUsage):
// a lifecycle that is switched off still shows what it knows.
func (s *Service) SetLifecycle(
	store skillusage.Lifecycle,
	settings config.SkillLifecycleConfig,
	archiveDir string,
) {
	s.lifecycle = store
	if store == nil || store.Empty() {
		s.lifecycle = nil
		s.curator = nil
		s.recording = false
		return
	}
	s.recording = settings.Enabled
	s.curator = NewCurator(s, store, settings, archiveDir)
}

// Curator returns the lifecycle curator, or nil when this runtime has
// no user database.
func (s *Service) Curator() *Curator { return s.curator }

// Lifecycle returns the wired lifecycle store, or nil.
func (s *Service) Lifecycle() skillusage.Lifecycle { return s.lifecycle }

// RecordUsage appends one activation event. Recording is best-effort by
// contract: a usage write must never fail a turn, so failures are
// logged and swallowed. A runtime without a user database, or one whose
// skill lifecycle is switched off, records nothing and stays silent.
func (s *Service) RecordUsage(ctx context.Context, event skillusage.Event) {
	if s.lifecycle == nil || !s.recording {
		return
	}
	if event.UsedAt.IsZero() {
		event.UsedAt = time.Now().UTC()
	}
	if err := s.lifecycle.Record(ctx, event); err != nil {
		telemetry.WarnErr(ctx, "skills: record usage failed", err,
			otellog.String("skill", event.Name))
	}
}

// retired reports whether one skill name is retired. Errors and a
// missing store both mean "not retired": the filter must never hide a
// skill because a query failed.
func (s *Service) retired(name string) bool {
	if s.lifecycle == nil {
		return false
	}
	return s.retiredSetCached()[name]
}

// retiredSetCached returns the retired-name set, re-reading it at most
// once per retiredCacheTTL.
func (s *Service) retiredSetCached() map[string]bool {
	s.retiredMu.Lock()
	defer s.retiredMu.Unlock()
	now := time.Now()
	if s.retiredDone && now.Sub(s.retiredAt) < retiredCacheTTL {
		return s.retiredSet
	}
	set, err := s.lifecycle.Retired(s.ctx)
	if err != nil {
		telemetry.WarnErr(s.ctx, "skills: read retired skills failed", err)
		// Keep serving the last known set rather than un-retiring
		// everything on a transient read failure.
		if s.retiredDone {
			return s.retiredSet
		}
		set = map[string]bool{}
	}
	s.retiredSet = set
	s.retiredAt = now
	s.retiredDone = true
	return set
}

// invalidateRetired drops the cached retired set so the next read sees
// a decision this service just made.
func (s *Service) invalidateRetired() {
	s.retiredMu.Lock()
	s.retiredDone = false
	s.retiredMu.Unlock()
}

// TopN returns the configured ranked-list size.
func (s *Service) TopN() int { return s.opts.TopN }

// MinScore returns the configured BM25 threshold.
func (s *Service) MinScore() float64 { return s.opts.MinScore }

// List returns all discovered skills sorted by name, then path. It is
// deliberately unfiltered: the skills page and the curator need to see
// retired skills (that is how they are restored). The activation and
// recommendation paths filter them out instead.
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

// Available returns the skills the registry still offers: List minus
// the retired ones. Everything that puts skills in front of the user
// or the model (the per-turn list, $mention resolution, skill_search)
// goes through this or through the equivalent filters in Mentioned /
// RankScored, so a retired skill cannot come back through a side door.
// List itself stays unfiltered: the skills page and the curator need to
// see retired skills, that is how they are restored.
func (s *Service) Available() []SkillMetadata {
	out := s.List()
	kept := out[:0]
	for _, sk := range out {
		if s.retired(sk.Name) {
			continue
		}
		kept = append(kept, sk)
	}
	return kept
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

// ByName resolves a skill by name. Duplicate names are resolved by
// Depth: scanned roots share depth 0 (ties keep the first path in the
// sorted skill list), builtins run at -1 and always lose.
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
// them to skills by Depth (in mention order). Retired skills are
// skipped: retiring one is what stops it from being injected.
func (s *Service) Mentioned(text string) []SkillMetadata {
	var out []SkillMetadata
	seen := map[string]bool{}
	for _, m := range mentionRe.FindAllStringSubmatch(text, -1) {
		sk, ok := s.ByName(m[1])
		if !ok || seen[sk.Path] || s.retired(sk.Name) {
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
			// Retired skills are not recommended any more. Filtering
			// after scoring keeps the BM25 threshold meaningful; the
			// price is that a retired hit is not backfilled, so the
			// list can come back shorter than the caller asked for.
			if s.retired(sk.Name) {
				continue
			}
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
	body, err := s.readBody(sk)
	if err != nil {
		return SkillMetadata{}, "", err
	}
	return sk, body, nil
}

// ReadByPath returns the metadata plus full SKILL.md body for one
// discovered skill, identified by the exact path returned from List.
// Like ReadFull it only serves paths in the current snapshot, so a
// stale path from the UI cannot read an arbitrary file on disk.
func (s *Service) ReadByPath(path string) (SkillMetadata, string, error) {
	snap := s.snapshot.Load()
	sk, ok := snap.byPath[path]
	if !ok {
		return SkillMetadata{}, "", fmt.Errorf("skills: %q not found", path)
	}
	body, err := s.readBody(sk)
	if err != nil {
		return SkillMetadata{}, "", err
	}
	return sk, body, nil
}

func (s *Service) readBody(sk SkillMetadata) (string, error) {
	if strings.HasPrefix(sk.Path, "builtin://") {
		body, err := readBuiltin(sk.Path)
		if err != nil {
			return "", fmt.Errorf("skills: read %s: %w", sk.Path, err)
		}
		if len(body) > maxSkillFileBytes {
			return "", fmt.Errorf(
				"skills: %s exceeds the %d-byte SKILL.md limit",
				sk.Path, maxSkillFileBytes)
		}
		return strings.TrimSpace(body), nil
	}
	snap := s.snapshot.Load()
	resolved, err := filepath.EvalSymlinks(sk.Path)
	if err != nil {
		return "", fmt.Errorf("skills: resolve %s: %w", sk.Path, err)
	}
	if !insideAnyRoot(resolved, snap.scanRoots) {
		return "", fmt.Errorf(
			"skills: %q resolves outside the configured skill roots", sk.Path)
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		return "", fmt.Errorf("skills: read %s: %w", sk.Path, err)
	}
	if len(data) > maxSkillFileBytes {
		return "", fmt.Errorf(
			"skills: %s exceeds the %d-byte SKILL.md limit",
			sk.Path, maxSkillFileBytes)
	}
	_, body, err := splitFrontmatter(data)
	if err != nil {
		return "", fmt.Errorf("skills: %s: %w", sk.Path, err)
	}
	return strings.TrimSpace(string(body)), nil
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

// Discover scans ~/.agents/skills, <userDir>/skills and any extra
// roots, collecting every SKILL.md. Parse failures are recorded in
// Errors, never fatal. Duplicate paths are de-duplicated.
func Discover(
	ctx context.Context,
	userDir string,
	extraRoots []string,
) SkillLoadOutcome {
	scans := buildScanRoots(userDir, extraRoots)
	c := collector{
		ctx:       ctx,
		seen:      map[string]bool{},
		foundRoot: map[string]bool{},
		roots:     cleanedRoots(scanRootPaths(scans)),
	}
	for _, scan := range scans {
		c.scanRoot(scan.path, scan.scope)
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

// scanRoot is one configured skill scan root.
type scanRoot struct {
	path  string
	scope string
}

func buildScanRoots(userDir string, extraRoots []string) []scanRoot {
	// Every scanned root shares depth 0: a duplicate name across roots
	// keeps the first path in the sorted skill list, and builtins
	// (depth -1) always lose.
	var scans []scanRoot
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

// insideAnyRoot reports whether resolved stays inside one of roots.
// Callers resolve symlinks first; the comparison is lexical.
func insideAnyRoot(resolved string, roots []string) bool {
	for _, root := range roots {
		if pathsafe.Within(root, resolved) {
			return true
		}
	}
	return false
}

type collector struct {
	ctx       context.Context
	seen      map[string]bool
	foundRoot map[string]bool
	roots     []string // cleaned scan roots for symlink containment
	skills    []SkillMetadata
	errors    []SkillError
}

// scanRoot BFS-walks one skill root, following symlinks (D2) and
// skipping hidden entries, collecting every SKILL.md.
func (c *collector) scanRoot(root string, scope string) {
	queue := []string{root}
	for len(queue) > 0 {
		dir := queue[0]
		queue = queue[1:]
		entries, err := os.ReadDir(dir)
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
				c.errors = append(c.errors, SkillError{
					Path: full, Message: w, Warning: true,
				})
			}
			sk := res.Metadata
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
	info, err := os.Stat(path)
	if err != nil {
		return ParseResult{}, err
	}
	if info.Size() > maxSkillFileBytes {
		return ParseResult{}, fmt.Errorf(
			"SKILL.md exceeds the %d-byte limit", maxSkillFileBytes)
	}
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
	if len(data) > maxSkillFileBytes {
		return ParseResult{}, fmt.Errorf(
			"%s exceeds the %d-byte SKILL.md limit",
			path, maxSkillFileBytes)
	}
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
// name (lowercase, hyphens), the fallback for third-party skills
// without a usable name field.
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

// sanitizeSingleLine folds all whitespace runs into single spaces.
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
