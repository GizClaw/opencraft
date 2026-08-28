// Shared DTO types mirroring internal/desktop/dto.go plus the
// flowcraft stream protocol wire shapes the frontend renders.

export interface ConfigStatus {
  needed: boolean;
  default_model: string;
  default_reasoning: boolean;
  work_dir: string;
  user_dir: string;
  version: string;
  agents: number;
}

export interface ProviderView {
  id: string;
  name: string;
  default_model: string;
  env_var: string;
  api: string;
  azure: boolean;
}

export interface ModelInstance {
  name: string;
  vision: boolean;
  reasoning: string;
  web_search: boolean;
}

export interface ProviderInstance {
  stable_id: string;
  type: string;
  name: string;
  api: string;
  key: string;
  key_set: boolean;
  key_env: boolean;
  key_keychain?: boolean;
  models: ModelInstance[];
  endpoint: string;
  enabled: boolean;
}

export interface InferenceRequest {
  instances: ProviderInstance[];
}

export interface ConfigState {
  instances: ProviderInstance[];
  model: string;
}

export interface ModelUsageStat {
  model: string;
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  reasoning_tokens: number;
  latency_ms: number;
  workspaces: number;
  sessions: number;
  updated_at: string;
}

export interface UsagePoint {
  time: string;
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
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
  messages: number;
  total_tokens: number;
}

export interface WorkspaceMeta {
  id: string;
  path: string;
  title: string;
  last_opened: string;
}

export interface ProjectConfigStatus {
  present: boolean;
  trusted: boolean;
  path?: string;
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

export type HistoryPart =
  | { type: 'text'; text?: string }
  | { type: 'reasoning'; text?: string }
  | {
      type: 'tool_call';
      call?: { id: string; name: string; arguments?: unknown };
    }
  | {
      type: 'tool_result';
      result?: { call_id: string; content?: string; is_error?: boolean };
    };

export interface KanbanCard {
  id: string;
  producer: string;
  consumer: string;
  status: string;
  target: string;
  input: string;
  output: string;
  caller: string;
  depth: number;
  error: string;
  run_id?: string;
  parent_run_id?: string;
  call_id?: string;
  created_at: string;
  updated_at: string;
}

export interface SkillDTO {
  name: string;
  description: string;
  scope: string;
  path: string;
}

export interface TurnStart {
  run_id: string;
  context_id: string;
}

export interface TurnEnd {
  run_id: string;
  status: string;
  error?: string;
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

export interface InteractDTO {
  id: string;
  run_id: string;
  conversation_id?: string;
  kind: string;
  title: string;
  body: StreamPart[];
  options: InteractOption[];
  multi: boolean;
  allow_other: boolean;
  source: string;
}

export interface StatusDTO {
  text: string;
  busy: boolean;
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

export interface ResolvedDTO {
  id: string;
  status: string;
  reason: string;
}

export interface FileNode {
  name: string;
  path: string;
  is_dir: boolean;
  size: number;
  children?: FileNode[];
}

export interface SearchFileHit {
  path: string;
  is_dir: boolean;
}

export interface UndoState {
  can_undo: boolean;
  can_redo: boolean;
}

export interface MemorySettings {
  max_raw_messages: number;
  preserve_recent: number;
  max_summary_bytes: number;
  replay_full_history: boolean;
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
  config_valid: boolean;
  config_error?: string;
  inference_configured: boolean;
  git_repo: boolean;
  git_branch?: string;
  session_count: number;
  active_runs: number;
  sandbox_backend: string;
  sandbox_available: boolean;
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

export interface AgentUpdateResult {
  name: string;
  description: string;
  persisted_to: string;
  created_at: string;
}

// ---- stream protocol ----

export interface ToolCallWire {
  id: string;
  name: string;
  arguments: unknown;
}

export interface ToolResultWire {
  call_id: string;
  content: string;
  is_error?: boolean;
}

export type StreamPart =
  | { type: 'text'; text: string }
  | { type: 'reasoning'; text?: string; signature?: string; id?: string }
  | { type: 'tool_call'; call: ToolCallWire }
  | { type: 'tool_result'; result: ToolResultWire }
  | { type: 'file'; uri?: string; name?: string; media_type?: string }
  | { type: 'image'; source?: unknown }
  | { type: 'data'; media_type?: string; value?: unknown };

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
