// Shared DTO types mirroring the Wails v3 desktop bindings plus the flowcraft
// stream protocol wire shapes the frontend renders.

export interface ConfigStatus {
  needed: boolean;
  default_model: string;
  default_reasoning: boolean;
  work_dir: string;
  user_dir: string;
  version: string;
  agents: number;
}

// ProviderView is one inference driver the settings page can build an
// instance from. Opencraft keeps no vendor table: the endpoint, the API
// surface, the wire dialect and the models are deployment data.
export interface ProviderView {
  id: string;
  name: string;
  env_var: string;
  model_endpoint: boolean;
  // Driver this entry registers (equals id today).
  impl: string;
}

// InferenceCatalogModel is one built-in model the settings page can
// prefill a model row from. Type names the inference driver that serves
// it (config.Providers); Model is the canonical declaration a save
// submits, so the catalog fills the row through the same shape the page
// already round-trips. Source records where the entry's facts came from
// and stays maintenance metadata.
export interface InferenceCatalogModel {
  id: string;
  type: string;
  vendor?: string;
  label?: string;
  source?: string;
  model: ModelSpec;
}

// InferenceCatalogTemplate is one built-in starter instance: the
// provider-level fields plus the models the new row starts with, in
// router priority order.
export interface InferenceCatalogTemplate {
  id: string;
  label: string;
  type: string;
  vendor?: string;
  api?: string;
  endpoint?: string;
  /** Provider-level knobs the template pins (e.g. the endpoint fact that
   * a chat deployment lowers video input). Empty means driver defaults. */
  advanced?: ProviderAdvanced;
  notes?: string;
  models: ModelSpec[];
}

export interface InferenceCatalogState {
  version: string;
  templates: InferenceCatalogTemplate[];
  models: InferenceCatalogModel[];
}

/** ReasoningCapability mirrors flowcraft's canonical capability DTO. */
export interface ReasoningCapability {
  kind?: string;
  effort_map?: Record<string, string>;
}

/** ModelCapabilities is the model's declared input/output/reasoning set. */
export interface ModelCapabilities {
  inputs?: string[];
  outputs?: string[];
  reasoning?: ReasoningCapability;
  hosted_web_search?: boolean;
  custom_embed_dimensions?: boolean;
}

export interface ModelLimits {
  max_input_tokens?: number;
  max_output_tokens?: number;
}

/**
 * ModelSpec is one model declaration. It is the canonical row shape,
 * shared by the settings page and by plugin-submitted deployments.
 */
export interface ModelSpec {
  name: string;
  kind?: string;
  capabilities?: ModelCapabilities;
  /** Per-model deployment address (ByteDance Ark ep-xxx ids). */
  endpoint?: string;
  limits?: ModelLimits;
  /** Discovery metadata; empty status means the model is active. */
  lifecycle?: ModelLifecycle;
  /** Driver-specific model leaves (resolution caps, wire-model aliases,
   * parameter matrices) as the JSON object the deployment declares. */
  driver_fields?: Record<string, unknown>;
}

export interface ModelLifecycle {
  status?: string;
  replacement_provider?: string;
  replacement_name?: string;
  notes?: string;
}

// AttachmentDTO mirrors the desktop binding's preview metadata for one
// local attachment. DataURL is present only for images (the preview
// channel WKWebView can render without file:// access). media_type
// describes the source file, while data_url may carry a re-encoded
// preview (e.g. large PNG normalized to JPEG), so callers must not
// assume the two media types agree.
export interface AttachmentDTO {
  name: string;
  path: string;
  size: number;
  media_type?: string;
  data_url?: string;
}

// AttachmentView is one attachment attached to a user message or
// staged in the composer. Images preview inline; everything else
// renders as a file chip. media_type identifies the file that is sent
// to the backend; data_url is only for inline preview and may use a
// different (normalized) type.
export interface AttachmentView {
  id: string;
  kind: 'image' | 'file' | 'audio' | 'video';
  path: string;
  name: string;
  media_type?: string;
  size?: number;
  data_url?: string;
}

/**
 * InstanceSpec is one inference deployment as submitted by a save. It is
 * the canonical row shape (config.InstanceSpec): a plugin submits the
 * same fields over inference.upsert.
 */
export interface InstanceSpec {
  stable_id?: string;
  type: string;
  name?: string;
  /** Driver impl for a vendor outside the preset catalog. */
  driver?: string;
  api?: string;
  endpoint?: string;
  /** env | literal | keychain; empty means "unchanged". */
  key_source?: string;
  /** Credential-store account of a keychain row. */
  key_ref?: string;
  /** Literal key typed into the form; write-only, never returned. */
  key_value?: string;
  /** User-owned; a plugin row is always enabled. */
  enabled?: boolean | null;
  advanced: ProviderAdvanced;
  models: ModelSpec[];
}

/**
 * ProviderInstance is one instance as the settings page reads it: the
 * canonical spec plus the computed credential/ownership flags.
 */
export interface ProviderInstance extends InstanceSpec {
  key_set: boolean;
  key_env: boolean;
  key_keychain?: boolean;
  managed: boolean;
}

// ProviderAdvanced mirrors the config layer's advanced provider spec
// knobs. Every field is optional: an empty value keeps the driver
// default. Which fields a driver reads differs — see the settings page
// section labels.
export interface ProviderAdvanced {
  routing?: string;
  query?: Record<string, string>;
  headers?: Record<string, string>;
  organization?: string;
  project?: string;
  timeout?: string;
  region?: string;
  auth_scheme?: string;
  auth_header?: string;
  /** "" keeps the default envelope; "-" disables forwarding. */
  metadata_envelope?: string;
  http_retries?: number;
  /** "" keeps the driver default; "true"/"false" send that value;
   * "omit" sends nothing for endpoints that do not know the field. */
  store?: string;
  /** Provider body fields the driver does not model: key to raw JSON. */
  extra_body?: Record<string, string>;
  include_reasoning_payload?: boolean;
  reasoning_channel?: string;
  reasoning_summary?: string;
  /** Verification scope of this deployment's reasoning traces. */
  reasoning_scope?: string;
  truncation?: string;
  chat_include_usage?: boolean;
  chat_include_obfuscation?: boolean;
  video_input?: boolean;
  media_base_url?: string;
  video_poll_interval_millis?: number;
}

export interface RouterPolicy {
  max_attempts: number;
  fallback_on_retry_exhausted: boolean;
}

export interface InferenceRequest {
  instances: InstanceSpec[];
  router: RouterPolicy;
}

export interface ConfigState {
  instances: ProviderInstance[];
  model: string;
  router: RouterPolicy;
}

export interface ModelUsageStat {
  model: string;
  total_tokens: number;
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
  reasoning_tokens: number;
  latency_ms: number;
  calls: number;
  workspaces: number;
  sessions: number;
  updated_at: string;
}

export interface UsagePoint {
  time: string;
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
  reasoning_tokens: number;
}

export interface PatchLineDTO {
  kind: 'context' | 'add' | 'delete';
  old_num?: number;
  new_num?: number;
  text: string;
}

export interface PatchFileDTO {
  path: string;
  action: string;
  added: number;
  removed: number;
  lines: PatchLineDTO[];
}

export interface ModelOption {
  id: string;
  label: string;
  reasoning: boolean;
}

export interface MCPServer {
  name: string;
  transport: string;
  command?: string;
  args?: string[];
  env?: Record<string, string>;
  url?: string;
}

export interface MCPStatus {
  name: string;
  status: 'connected' | 'connecting' | 'error';
  error?: string;
}

export interface SessionMeta {
  id: string;
  title: string;
  created_at: string;
  updated_at: string;
  turns: number;
  messages: number;
  total_tokens: number;
}

export interface SessionImportDTO {
  session_id: string;
  messages: number;
  turns: number;
}

export interface WorkspaceMeta {
  id: string;
  path: string;
  title: string;
  last_opened: string;
}

// HistoryMessage is the wire form of flowcraft's message.Message: the
// resume view gets the same ordered parts (text, reasoning, tool
// calls, tool results) the live stream renders.
export interface HistoryMessage {
  role: string;
  content?: {
    parts?: HistoryPart[];
  };
}

// ArtifactDTO is one file a turn produced, persisted with the turn
// archive and reported live as "artifact" UI events.
export interface ArtifactDTO {
  path: string;
  bytes?: number;
}

// SessionTurn is one archived turn: its messages plus the artifacts it
// produced, so resuming renders one artifact strip per turn.
export interface SessionTurn {
  seq: number;
  at: string;
  requested_at?: string;
  started_at?: string;
  finished_at?: string;
  duration_ms?: number;
  run_id?: string;
  status?: string;
  error?: string;
  // interrupt_cause / error_kind are the structured class of a failed
  // turn (engine interrupt cause, inference error kind, or the
  // harness-level timeout); the transcript renders its copy from these
  // instead of parsing error.
  interrupt_cause?: string;
  error_kind?: string;
  request_id?: string;
  response_id?: string;
  messages: HistoryMessage[];
  artifacts?: ArtifactDTO[];
}

export type HistoryPart =
  | { type: 'text'; text?: string }
  | { type: 'reasoning'; text?: string }
  | {
      type: 'tool_call';
      call?: { id: string; name: string; arguments?: unknown };
    }
  | {
      type: 'tool_result';
      result?: ToolResultWire;
    }
  | { type: 'image'; source?: MediaSourceWire }
  | {
      type: 'audio';
      source?: MediaSourceWire;
      format?: unknown;
      duration_millis?: number;
    }
  | { type: 'video'; source?: MediaSourceWire }
  | { type: 'file'; uri?: string; media_type?: string; name?: string };

export interface SkillDTO {
  name: string;
  description: string;
  scope: string;
  path: string;
  plugin_id?: string;
  plugin_name?: string;
}

export interface TurnStart {
  run_id: string;
  conversation_id: string;
  requested_at?: string;
  started_at?: string;
}

export interface SessionSnapshot {
  session_id: string;
  mode: string;
  think: string;
  model: string;
}

export interface SessionDefaults {
  mode: string;
  think: string;
}

// PetsSettings mirrors the Go pet preference document (desktop.json).
export interface PetsSettings {
  enabled: boolean;
  assistantCharacter?: string;
}

// ActiveRunDTO mirrors App.ActiveRun: the run id currently executing
// in one conversation, or empty when the conversation is idle.
export interface ActiveRunDTO {
  run_id?: string;
}

export interface ReplyRequest {
  text: string;
  option?: string | null;
  options?: string[];
  cancel?: boolean;
}

export interface InteractOption {
  label: string;
  value: string;
}

// How loudly one interaction is presented: info asks a question,
// notice grants something (permissions, an allowlist entry) and
// danger widens access beyond the sandbox. The host decides it; the UI
// only styles it.
export type InteractSeverity = 'info' | 'notice' | 'danger';

export interface InteractDTO {
  id: string;
  run_id: string;
  conversation_id?: string;
  kind: string;
  severity: InteractSeverity;
  title: string;
  body: StreamPart[];
  options: InteractOption[];
  multi: boolean;
  allow_other: boolean;
  source: string;
}

export interface UsageDTO {
  model: string;
  input_tokens: number;
  output_tokens: number;
  total_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
  reasoning_tokens: number;
  latency_ms: number;
}

export interface FileNode {
  name: string;
  path: string;
  is_dir: boolean;
  size: number;
}

// ResolvedTarget mirrors the File.ResolveTarget binding result: one
// containment-checked local file or directory.
export interface ResolvedTarget {
  path: string;
  rel: string;
  root: string;
  name: string;
  is_dir: boolean;
  size: number;
  media_type?: string;
}

// FilePreview mirrors the File.ReadPreview binding result. Kind tells
// the viewer how to render: text/image/pdf/video, or meta for files
// that fall back to the system app.
export interface FilePreview {
  path: string;
  rel: string;
  root: string;
  name: string;
  size: number;
  media_type: string;
  kind: 'text' | 'image' | 'pdf' | 'video' | 'meta';
  text?: string;
  data_url?: string;
  /** Loopback URL a video kind streams from (byte ranges, no base64). */
  stream_url?: string;
  too_large?: boolean;
}

// FileTab is one open file viewer tab. Viewer state is memory-only
// and scoped per conversation; it lives until the app reloads.
export interface FileTab {
  key: string;
  path: string;
  rel: string;
  root: string;
  name: string;
  media_type: string;
}

export interface SearchFileHit {
  path: string;
  is_dir: boolean;
}

export interface GitRepo {
  in_repo: boolean;
  root?: string;
  branch?: string;
  upstream?: string;
  ahead: number;
  behind: number;
  workspace?: string;
}

export type GitChangeKind =
  | 'modified'
  | 'added'
  | 'deleted'
  | 'renamed'
  | 'copied'
  | 'typechange'
  | 'untracked'
  | 'unmerged';

export interface GitChange {
  path: string;
  orig_path?: string;
  kind: GitChangeKind;
  staged: boolean;
  unstaged: boolean;
  untracked: boolean;
  unmerged: boolean;
  directory: boolean;
  is_binary: boolean;
  additions: number;
  deletions: number;
  in_workspace: boolean;
}

export interface GitStatus {
  root?: string;
  workspace?: string;
  branch?: string;
  truncated: boolean;
  entries: GitChange[];
}

export interface GitLogEntry {
  oid: string;
  short_oid: string;
  author: string;
  date: string;
  subject: string;
}

export interface GitCommitFile {
  path: string;
  orig_path?: string;
  kind: GitChangeKind;
  additions: number;
  deletions: number;
  is_binary: boolean;
}

export interface GitCommitFiles {
  files: GitCommitFile[];
  truncated: boolean;
}

export interface GitBranch {
  name: string;
  current: boolean;
  upstream?: string;
}

export interface GitDiff {
  content: string;
  truncated: boolean;
}

export interface GitHubAuthor {
  login: string;
  avatar_url?: string;
}

export interface PRAvailability {
  available: boolean;
}

export interface GitHubPull {
  number: number;
  title: string;
  state: 'open' | 'closed' | 'merged';
  draft: boolean;
  author: GitHubAuthor;
  base: string;
  head: string;
  updated_at: string;
  html_url: string;
}

export interface GitHubPRCommit {
  sha: string;
  short_sha: string;
  message: string;
  author: string;
  date: string;
}

export interface GitHubCheck {
  name: string;
  kind: 'check_run' | 'status';
  state: string;
  description?: string;
  url?: string;
}

export interface GitHubComment {
  id: number;
  author: GitHubAuthor;
  body: string;
  created_at: string;
  html_url: string;
}

export interface GitHubTimelineItem {
  id: number;
  kind: 'comment' | 'review';
  author: GitHubAuthor;
  body: string;
  action?: string;
  created_at: string;
  html_url: string;
}

export interface GitHubReviewThread {
  path: string;
  side?: 'LEFT' | 'RIGHT';
  line: number;
  original_line: number;
  start_line?: number;
  original_start_line?: number;
  diff_hunk: string;
  comments: GitHubComment[];
}

export interface GitHubPRDetail {
  number: number;
  title: string;
  state: 'open' | 'closed' | 'merged';
  draft: boolean;
  author: GitHubAuthor;
  base: string;
  head: string;
  head_sha: string;
  created_at: string;
  updated_at: string;
  html_url: string;
  body: string;
  mergeable: boolean;
  mergeable_state: string;
  changed_files: number;
  additions: number;
  deletions: number;
  commits_count: number;
  commits: GitHubPRCommit[];
  checks: GitHubCheck[];
  conversation: GitHubTimelineItem[];
  threads: GitHubReviewThread[];
  truncated: boolean;
}

export interface MemorySettings {
  max_raw_messages: number;
  preserve_recent: number;
  max_summary_bytes: number;
  replay_full_history: boolean;
}

/**
 * ToolOptionFieldView is one provider-specific knob the tools settings
 * tab renders; the host owns the vocabulary, the label comes from i18n.
 */
export interface ToolOptionFieldView {
  name: string;
  kind: 'bool' | 'int' | 'float' | 'enum' | 'string';
  values?: string[] | null;
  min?: number | null;
  max?: number | null;
  exclusive_min?: boolean;
  exclusive_max?: boolean;
  /**
   * default is the provider default the driver documents; the page shows
   * it as a hint while the knob stays unset.
   */
  default?: string | null;
}

/**
 * ToolOptionPresetView is a user-invoked starting point: the page fills
 * its values into the form and nothing is stored until the user saves.
 */
export interface ToolOptionPresetView {
  id: string;
  fields: { [field: string]: unknown };
}

/** ToolOptionInstanceView is one deployment a tool card configures. */
export interface ToolOptionInstanceView {
  id: string;
  label: string;
  impl: string;
  managed: boolean;
  fields: ToolOptionFieldView[] | null;
  /** The driver's one-click presets, in declaration order. */
  presets?: ToolOptionPresetView[] | null;
  /** Configured knob values keyed by dotted field path. */
  values: { [key: string]: unknown } | null;
}

/** ToolOptionsToolView is one generation tool's card. */
export interface ToolOptionsToolView {
  instances: ToolOptionInstanceView[] | null;
}

/** ToolOptionsState is the whole provider-specific tools configuration. */
export interface ToolOptionsState {
  image: ToolOptionsToolView;
  video: ToolOptionsToolView;
}

/** ToolOptionsRequest is the flat dotted save payload per tool. */
export interface ToolOptionsRequest {
  image: { [id: string]: { [field: string]: unknown } };
  video: { [id: string]: { [field: string]: unknown } };
}

/** WebSearchProviderView is one provider row of the web search card. */
export interface WebSearchProviderView {
  id: string;
  keyless: boolean;
  key_set: boolean;
  endpoint: string;
  override?: string;
}

/** WebSearchEndpoints carries the optional per-provider endpoint overrides. */
export interface WebSearchEndpoints {
  exa?: string;
  parallel?: string;
  tavily?: string;
  brave?: string;
}

/**
 * WebSearchState is the persisted web search configuration plus, per
 * provider, whether a credential is present and which endpoint is in
 * effect.
 */
export interface WebSearchState {
  enabled: boolean;
  provider: string;
  max_results: number;
  timeout: string;
  endpoints: WebSearchEndpoints;
  providers: WebSearchProviderView[];
  secrets_available: boolean;
}

/** WebSearchRequest is the save payload for the web search card. */
export interface WebSearchRequest {
  enabled: boolean;
  provider: string;
  max_results: number;
  timeout: string;
  endpoints: WebSearchEndpoints;
  keys?: { [provider: string]: string };
  clearKeys?: string[];
}

/** WebSearchTestRequest runs one real query through one provider. */
export interface WebSearchTestRequest {
  provider: string;
  query: string;
  apiKey?: string;
  endpoint?: string;
  maxResults?: number;
}

/** WebSearchTestHit is one result of a test run. */
export interface WebSearchTestHit {
  title?: string;
  url: string;
  snippet?: string;
  published?: string;
}

/** WebSearchTestResult reports one real provider call. */
export interface WebSearchTestResult {
  provider: string;
  results: WebSearchTestHit[];
  context?: string;
}

/**
 * TelemetryExportStatus is the OTLP export sink the app runs. Endpoint
 * and header names are visible; header values are credentials and never
 * leave the backend.
 */
export interface TelemetryExportStatus {
  /** User switch allowing capability plugins to install export sinks. */
  enabled: boolean;
  /** Whether any OTLP endpoint is active, whoever installed it. */
  configured: boolean;
  endpoint: string;
  insecure: boolean;
  headerNames: string[];
  /** Plugin that installed the active sink; empty for app configuration. */
  owner: string;
}

/**
 * HTTPProbeStatus is the provider round-trip probe behind the DEV
 * switch. It wraps the process HTTP transport so every provider call
 * leaves two records — when the request left the process and when the
 * response headers arrived — which is what splits a slow step into
 * local assembly and provider time. env reports the
 * OPENCRAFT_HTTP_PROBE override, and blocker names the MCP
 * configuration that keeps the probe out (the streamable-HTTP MCP
 * client cannot run under a wrapped transport).
 */
export interface HTTPProbeStatus {
  /** The persisted DEV switch (desktop.json diagnostics.httpProbe). */
  enabled: boolean;
  /** OPENCRAFT_HTTP_PROBE forces the probe on. */
  env: boolean;
  /** Whether the transport is wrapped right now. */
  active: boolean;
  /** Why the probe is parked, when it is; empty otherwise. */
  blocker: string;
}

/**
 * PathSegment is one entry of the process PATH the app runs with.
 * "prepend" is a directory the user configured, "inherited" is what the
 * app was launched with, and "candidate" is a standard install directory
 * the host appended because the inherited PATH lacked it.
 */
export interface PathSegment {
  dir: string;
  source: 'prepend' | 'inherited' | 'candidate' | string;
  present: boolean;
}

/**
 * ExecPool is the diagnostics view of the exec supervisor pool: the
 * persisted knobs plus the live idle/active child counts.
 */
export interface ExecPool {
  prewarm: number;
  maxIdle: number;
  maxActive: number;
  idleMinutes: number;
  idle: number;
  active: number;
}

/**
 * PathEnvironment is the diagnostics view of the PATH every command, MCP
 * server and sandbox child inherits. The desktop app is usually started
 * from Finder/Dock, which inherits launchd's minimal PATH instead of the
 * shell's, so this is where a missing tool becomes visible.
 */
export interface PathEnvironment {
  path: string;
  segments: PathSegment[];
  /** Persisted override directories, i.e. the editor's value. */
  prepend: string[];
  /** Override entries dropped because they are not absolute directories. */
  rejected: string[];
  /** Candidate directories that do not exist on this machine. */
  missing: string[];
  /** Whether the runtime reloaded so MCP servers re-attach. */
  reloaded: boolean;
}

/**
 * Recovery is the diagnostics view of crash recovery: what the last pass
 * did for the active workspace (see internal/orchestration/host/recover.go)
 * and how many run checkpoints its store is holding now. A turn drops its
 * checkpoint once it is archived, so rows left behind are a live run or a
 * turn the next assembly has to reconstruct.
 */
export interface Recovery {
  /** The workspace whose store these numbers came from. */
  workspace?: string;
  /** Whether an assembly ran a pass in this process. */
  ran: boolean;
  /** When that pass ran (RFC3339). */
  at?: string;
  recovered: number;
  archived: number;
  discarded: number;
  skipped_live: number;
  /**
   * The live process holding this workspace's lock, when another one does:
   * no pass ran here and the checkpoints wait for the next owner.
   */
  workspace_holder?: string;
  failed: number;
  pending: number;
  /** Every row of the checkpoint table. */
  checkpoint_rows: number;
  /** The assistant-run subset the pass examines. */
  checkpoint_runs: number;
  /** Encoded size of those rows. */
  checkpoint_bytes: number;
}

export interface DiagnosticsReport {
  version: string;
  go_version: string;
  node_version: string;
  git_version: string;
  platform: string;
  arch: string;
  work_dir: string;
  user_dir: string;
  /** State root: sessions, user.db, logs, cache (one GUI per root). */
  data_dir?: string;
  /** Shared content/credential root (keyring, plugins, agents). */
  app_home?: string;
  /** State-root profile name; empty is the default instance. */
  profile?: string;
  config_valid: boolean;
  config_error?: string;
  inference_configured: boolean;
  git_repo: boolean;
  git_branch?: string;
  session_count: number;
  active_runs: number;
  sandbox_backend: string;
  sandbox_available: boolean;
  /** Shell exec_command spawns through (e.g. "/bin/sh -c"). */
  exec_shell: string;
  usage_total_tokens: number;
}

export interface SandboxProbeResult {
  ok: boolean;
  output?: string;
  error?: string;
}

export interface PolicyDecision {
  command: string;
  allowed: boolean;
  rules: string[];
}

export interface CacheClearResult {
  dirs: string[];
  bytes: number;
}

// ConfigCompatRepair is the outcome of the diagnostics compatibility
// repair: `removed` lists the user-layer declarations that referenced
// retired assembly variables, `backup` is the pre-repair copy.
export interface ConfigCompatRepair {
  file: string;
  backup: string;
  // Null when the layer had nothing to repair: Go marshals a nil slice
  // as null, so callers must default to an empty list.
  removed: string[] | null;
}

export interface AgentSummary {
  name: string;
  description: string;
  created_at?: string;
}

export interface GraphNodeDTO {
  id: string;
  type: string;
  config?: Record<string, unknown>;
}

export interface GraphEdgeDTO {
  from: string;
  to: string;
  condition?: string;
}

export interface GraphDTO {
  name: string;
  entry: string;
  nodes: GraphNodeDTO[];
  edges: GraphEdgeDTO[];
}

export interface AgentDetail {
  name: string;
  description: string;
  graph: GraphDTO;
  created_at?: string;
}

// ---- stream protocol ----

export interface ToolCallWire {
  id: string;
  name: string;
  arguments: unknown;
}

// ContentWire is the wire form of flowcraft's message.Content: the
// ordered parts an operation produced.
export interface ContentWire {
  parts?: StreamPart[];
}

export interface ToolResultWire {
  call_id: string;
  // content is the tool's canonical payload. Tools answer with the
  // parts the model saw — every built-in tool returns one text part
  // holding its JSON envelope — so the card renders the text of those
  // parts instead of a single flattened string.
  content?: ContentWire;
  is_error?: boolean;
}

export type StreamPart =
  | { type: 'text'; text: string }
  | { type: 'reasoning'; text?: string; signature?: string; id?: string }
  | { type: 'tool_call'; call: ToolCallWire }
  | { type: 'tool_result'; result: ToolResultWire }
  | { type: 'file'; uri?: string; name?: string; media_type?: string }
  | { type: 'image'; source?: MediaSourceWire }
  | {
      type: 'audio';
      source?: MediaSourceWire;
      format?: unknown;
      duration_millis?: number;
    }
  | { type: 'video'; source?: MediaSourceWire }
  | { type: 'data'; media_type?: string; value?: unknown };

// MediaSourceWire is the wire form of flowcraft's media source: a
// local/remote URL, or inline base64 bytes.
export interface MediaSourceWire {
  kind: 'url' | 'inline' | 'stream';
  url?: string;
  data?: string;
  media_type?: string;
}

// TurnMessage is the wire form of message.Message the frontend sends
// to StartTurn: role + type-discriminated content parts.
export interface TurnMessage {
  role: string;
  content: { parts: StreamPart[] };
}

export interface StreamDelta {
  type:
    | 'part'
    | 'finish'
    | 'provider_outputs'
    | 'parallel_branch_accept'
    | 'parallel_branch_cancel'
    | string;
  part?: StreamPart;
  finish_reason?: string;
  request_id?: string;
  response_id?: string;
  speculative?: boolean;
  fork_id?: string;
  branch_id?: string;
  reason?: string;
  provider_outputs?: unknown[];
  payload?: unknown;
}

export interface UIEvent {
  type: string;
  data: unknown;
}

// Automation types mirror internal/adapters/desktop/bindings/automation.go
// DTOs.

export interface AutomationSchedule {
  type: string;
  interval_hours?: number;
  interval_weeks?: number;
  days?: string[];
  time?: string;
}

export interface AutomationTask {
  id: string;
  name: string;
  prompt: string;
  schedule: AutomationSchedule;
  workspace: string;
  mode: string;
  model: string;
  think: string;
  conversation_id?: string;
  notify: string;
  timeout: string;
  enabled: boolean;
  created_at: string;
  updated_at: string;
  last_run_at: string;
  last_status: string;
  next_run_at: string;
}

export interface AutomationRun {
  id: string;
  task_id: string;
  at: string;
  status: string;
  error: string;
  conversation_id: string;
  run_id: string;
  duration_ms: number;
  summary: string;
}
