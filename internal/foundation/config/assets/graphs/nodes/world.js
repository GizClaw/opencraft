// Seeds the model-facing channel: the world state (instructions,
// environment, permissions, memory, the replayed prefix) rendered into
// board vars by the opencraft.prepare hook, then this turn's own
// messages, with the per-turn context block riding the user's message.
//
// The merge itself is Go (internal/capabilities/worldstate/merge.go,
// reached through the "ocworld" script global): the node rewrites the
// whole transcript every turn, and doing that through the script bridge
// cost a JSON projection per message in each direction — ~70 ms per
// turn at 2 MB of conversation, ~280 ms at 8 MB, all of it before the
// model sees a token. Keep this node's board access in ocworld: reading
// or rewriting the channel from here would bring that cost straight
// back.
ocworld.merge();
