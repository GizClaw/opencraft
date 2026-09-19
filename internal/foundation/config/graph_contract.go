package config

// CompactArchiveChannel is the board channel the assistant graph's
// compact node moves folded conversation into before it shrinks the
// MainChannel. It is a contract between the graph asset
// (assets/graphs/nodes/compact.js) and the memory hooks that archive a
// turn: the hooks concatenate this channel with MainChannel so a
// compacted turn still persists every message it exchanged. The name
// deliberately avoids the engine's "__" namespace.
const CompactArchiveChannel = "opencraft.compact_archive"
