import { itemText, type MessageView } from './store';

// ThinkSnapshot is the newest reasoning block of a transcript.
export interface ThinkSnapshot {
  // ID identifies the block: a new id is a new thought, which is what
  // the card's "something new is happening" revival keys on. The id
  // survives the streaming appends (the block keeps its identity while
  // its chunks grow).
  id: string;
  text: string;
}

// latestThink returns the newest reasoning block, or null when the
// transcript has none (a non-reasoning model, think off, or a session
// that never reasoned). Finished turns keep their last block, so the
// card can show the model's last thought until the next one replaces
// it; the scan is tail-first and bounded by the transcript's items
// (MAX_ITEMS_PER_MESSAGE caps every message), which is small next to
// the transcript render it runs beside.
export function latestThink(messages: MessageView[]): ThinkSnapshot | null {
  for (let i = messages.length - 1; i >= 0; i--) {
    const items = messages[i].items;
    for (let j = items.length - 1; j >= 0; j--) {
      const item = items[j];
      if (item.kind !== 'reasoning') continue;
      // An empty block (created but not fed yet) falls through to the
      // previous thought instead of blanking the section.
      const text = itemText(item);
      if (text) return { id: item.id, text };
    }
  }
  return null;
}
