// The graph editor's field catalog: which config keys each node type accepts,
// and the labels used to render them.
//
// This lives apart from GraphView.tsx on purpose. It is data, not view: the
// contract test that keeps it honest (GraphView.catalog.test.ts) compares it
// against the node scripts' `cfg.*` reads, and importing the component module
// would pull in `@wailsio/runtime`, whose drag module starts a 50ms poll that
// outlives a short-lived test environment (`window is not defined` after
// teardown). A pure module keeps both directions checkable.
//
// The catalog is hand-maintained and the node scripts are the only readers of
// most of these keys: a key a node does not read is a silent no-op for whoever
// sets it in the panel (the editor suggests the field, the node ignores it),
// and a knob the node reads but the catalog lacks can only be reached through
// the raw JSON editor. NESTED_FIELDS and FIELD_CATALOGS are exported for that
// test; keep them in step with the nodes when either side changes.

// FieldSpec describes one valid config field of a node type (mirrors the
// flowcraft node config structs). The editor uses the catalog to offer fields
// that are not yet present in the node's config.
export type FieldSpec = {
  key: string;
  kind: 'string' | 'number' | 'bool' | 'array' | 'object';
};

export const FIELD_CATALOGS: Record<string, FieldSpec[]> = {
  inference: [
    { key: 'model', kind: 'object' },
    { key: 'model_hint', kind: 'string' },
    { key: 'messages_channel', kind: 'string' },
    { key: 'system_prompt', kind: 'string' },
    { key: 'output_key', kind: 'string' },
    { key: 'usage_key', kind: 'string' },
    { key: 'tool_pending_key', kind: 'string' },
    { key: 'undefined_tool_recovery', kind: 'object' },
    { key: 'recover_pending_key', kind: 'string' },
    { key: 'recover_count_key', kind: 'string' },
    { key: 'stream', kind: 'bool' },
    { key: 'tools', kind: 'array' },
    { key: 'all_tools', kind: 'bool' },
    { key: 'tool_choice', kind: 'object' },
    { key: 'intent', kind: 'object' },
    { key: 'extensions', kind: 'array' },
  ],
  tool: [
    { key: 'messages_channel', kind: 'string' },
    { key: 'results_key', kind: 'string' },
  ],
  script: [
    { key: 'runtime', kind: 'string' },
    { key: 'name', kind: 'string' },
    { key: 'source', kind: 'string' },
    { key: 'config', kind: 'object' },
  ],
};

// Nested catalogs for object fields whose contents are also known.
export const NESTED_FIELDS: Record<string, FieldSpec[]> = {
  'inference.undefined_tool_recovery': [
    { key: 'enabled', kind: 'bool' },
    { key: 'max_per_run', kind: 'number' },
  ],
  'script.config': [
    { key: 'preserve_recent', kind: 'number' },
    { key: 'budget_chars', kind: 'number' },
    { key: 'threshold_ratio', kind: 'number' },
    { key: 'max_consecutive_failures', kind: 'number' },
    { key: 'max_folds_per_turn', kind: 'number' },
    { key: 'max_input_tokens', kind: 'number' },
    { key: 'system_prompt_tokens', kind: 'number' },
  ],
};

// Common field labels; anything not listed falls back to the raw key.
export const FIELD_LABELS: Record<string, string> = {
  model_hint: 'graph.modelHint',
  all_tools: 'graph.allTools',
  stream: 'graph.stream',
  reasoning_effort: 'graph.reasoningEffort',
  max_per_run: 'graph.maxPerRun',
  tool_pending_key: 'graph.toolPendingKey',
  recover_pending_key: 'graph.recoverPendingKey',
  recover_count_key: 'graph.recoverCountKey',
  runtime: 'graph.runtime',
  preserve_recent: 'graph.preserveRecent',
  budget_chars: 'graph.budgetChars',
  threshold_ratio: 'graph.thresholdRatio',
  max_consecutive_failures: 'graph.maxConsecutiveFailures',
  max_folds_per_turn: 'graph.maxFoldsPerTurn',
  max_input_tokens: 'graph.maxInputTokens',
  system_prompt_tokens: 'graph.systemPromptTokens',
  results_key: 'graph.resultsKey',
};

// defaultFor seeds a newly added field with the kind's empty value.
export function defaultFor(spec: FieldSpec): unknown {
  switch (spec.kind) {
    case 'bool':
      return false;
    case 'number':
      return 0;
    case 'array':
      return [];
    case 'object':
      return {};
    default:
      return '';
  }
}
