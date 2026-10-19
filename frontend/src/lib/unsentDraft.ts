// unsentDraft answers whether the window's composer holds work that only
// exists in this document: the text the reader has not sent, and the
// attachments staged beside it. The memory boundary asks before it hands
// the page back to WebKit (see shell/workspaces.ts): the reload is
// best-effort by design, and a switch that would drop a draft is one it
// skips.
//
// The attachment half is not about the bytes - a pasted image is already
// on disk (`File.ImportPastedImage` writes it, and the staged view
// carries its path) - but about the tray: which files are staged, in what
// order, with which previews. That list is the composer's state, and a
// reload would hand the reader an empty tray and a directory to search
// for what they just pasted.
//
// The registry is a module, not store state, for the same reason the
// caches are: nothing renders it. ChatView publishes what its own state
// holds on every change, and a reader who asks why a switch kept the page
// finds the answer here.

let pending = false;

// setUnsentDraft publishes the composer's state. Called on every change of
// the composer, and with false when the view that held it unmounts.
export function setUnsentDraft(next: boolean): void {
  pending = next;
}

// hasUnsentDraft is what a document swap would throw away.
export function hasUnsentDraft(): boolean {
  return pending;
}
