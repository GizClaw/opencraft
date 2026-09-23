import { conversationWorkspace } from '../state/react';
import { pendingConversationIDs } from './store';
import type { SessionMeta } from './types';

// The session slots: the first rows of the sidebar's session list, bound to
// Mod+1 … Mod+4 (`session.slot1` in lib/keys.ts).
//
// This module owns their one definition, because two surfaces have to agree
// about it: the sidebar numbers the rows it draws and the dispatcher resumes
// the row a digit names. A second definition would be a key that lands
// somewhere other than the number the user read off the screen.
//
// "The row the user read" is the *visible* order, which is not the archive
// order: a conversation that is running — or waiting on a prompt — leads the
// list of its workspace, so the digits follow the sidebar's running-first
// order and shift when such a row appears. That is the point of numbering
// the rows: the number is where the digit goes, not a rank in the archive.

/** How many session slots the keyboard binds (Mod+1 … Mod+4). */
export const SESSION_SLOTS = 4;

/**
 * runningIDsByWorkspace groups the live conversations by the workspace that
 * owns them, in the sidebar's order: turns that are running, then
 * conversations waiting on a prompt. A conversation with no workspace (a run
 * whose host has no window) is listed nowhere, exactly as in the sidebar.
 */
export function runningIDsByWorkspace(
  runningIDs: string[],
  pendingPromptConvs: Record<string, string>,
): Record<string, string[]> {
  const byPath: Record<string, string[]> = {};
  for (const id of runningIDs) {
    const path = conversationWorkspace(id);
    if (!path) continue;
    (byPath[path] ??= []).push(id);
  }
  for (const id of pendingConversationIDs(pendingPromptConvs)) {
    const path = conversationWorkspace(id);
    if (!path || byPath[path]?.includes(id)) continue;
    (byPath[path] ??= []).push(id);
  }
  return byPath;
}

/**
 * sessionSlotIDs returns the session each slot of one workspace names: the
 * running rows first, then the stored rows in the order the store keeps them
 * (newest activity first). Entries past the end of the list are undefined —
 * an empty slot is a key that does nothing.
 */
export function sessionSlotIDs(
  sessions: SessionMeta[],
  runningIDs: string[],
  count: number = SESSION_SLOTS,
): Array<string | undefined> {
  const running = new Set(runningIDs);
  const rows = [
    ...runningIDs,
    ...sessions
      .filter((session) => !running.has(session.id))
      .map((session) => session.id),
  ];
  const slots: Array<string | undefined> = [];
  for (let index = 0; index < count; index += 1) slots.push(rows[index]);
  return slots;
}
