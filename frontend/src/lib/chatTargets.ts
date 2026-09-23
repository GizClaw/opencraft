// The chat surface's imperative capabilities, published to the shell.
//
// Three shortcuts (focus the composer, walk the transcript by user
// message, copy the last reply) mean nothing outside the transcript, but
// they are also reachable from the command palette and from the native
// macOS menu, whose handlers live in the shell and must not hold a ref to
// a component they do not own. ChatView therefore publishes what it owns
// while it is mounted, and every entry point calls through this slot.
//
// One slot, not a listener list: the app mounts exactly one chat surface,
// and a stale publisher is a bug (it would answer for a transcript that
// left the screen). setChatTargets(null) on unmount is what keeps the
// answers honest.
export interface ChatTargets {
  /** Put the caret in the composer (⌘L). */
  focusComposer: () => void;
  /** Scroll to the previous (−1) or next (+1) user message (⌘↑/⌘↓). */
  walkUserMessage: (direction: 1 | -1) => void;
  /** Copy the newest assistant reply to the clipboard (⌘⇧C). */
  copyLastReply: () => void;
}

let targets: ChatTargets | null = null;

export function setChatTargets(next: ChatTargets | null): void {
  targets = next;
}

export function chatTargets(): ChatTargets | null {
  return targets;
}
