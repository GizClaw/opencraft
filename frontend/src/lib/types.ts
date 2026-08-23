// Shared DTO types mirroring internal/desktop/dto.go plus the
// flowcraft stream protocol wire shapes the frontend renders.

export interface ConfigStatus {
  needed: boolean;
  default_model: string;
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

export interface SetupProvider {
  id: string;
  key: string;
  key_env: boolean;
  model: string;
  endpoint: string;
  vision: boolean;
  reasoning: string;
  web_search: boolean;
}

export interface SetupRequest {
  providers: SetupProvider[];
}

export interface ConfigState {
  providers: SetupProvider[];
  model: string;
}

export interface ModelOption {
  id: string;
  label: string;
}

export interface SessionMeta {
  id: string;
  title: string;
  created_at: string;
  updated_at: string;
  messages: number;
  total_tokens: number;
}

export interface HistoryMsg {
  role: string;
  text: string;
}

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

export interface AgentSummary {
  name: string;
  description: string;
  created_at?: string;
}

// ---- stream protocol ----

export interface ToolCallWire {
  id: string;
  name: string;
  arguments: string;
}

export interface ToolResultWire {
  call_id: string;
  content: string;
  is_error?: boolean;
}

export type StreamPart =
  | { type: "text"; text: string }
  | { type: "reasoning"; text?: string; signature?: string; id?: string }
  | { type: "tool_call"; call: ToolCallWire }
  | { type: "tool_result"; result: ToolResultWire }
  | { type: "file"; uri?: string; name?: string; media_type?: string }
  | { type: "image"; source?: unknown }
  | { type: "data"; media_type?: string; value?: unknown };

export interface StreamDelta {
  type:
    | "part"
    | "finish"
    | "provider_outputs"
    | "parallel_branch_accept"
    | "parallel_branch_cancel"
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
