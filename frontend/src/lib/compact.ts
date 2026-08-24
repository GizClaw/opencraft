// Compact summary marker. Must match compact.SummaryPrefix
// (internal/tools/compact/compact.go) and its mirror in
// internal/config/assets/graphs/nodes/compact.js: the compaction node
// appends the summary as a user message starting with this prefix, and
// lifecycle hooks filter it out of persistence. The chat renders such
// messages as a compact card instead of a plain user bubble.
export const COMPACT_SUMMARY_PREFIX =
  "Another language model started to solve this problem and produced a summary of its thinking process.";
