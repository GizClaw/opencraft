import { sanitizeToolResult } from '../ansi';
import { COMPACT_SUMMARY_PREFIX } from '../compact';
import { toolResultImages, toolResultText } from '../toolresult';
import type {
  AttachmentView,
  HistoryMessage,
  HistoryPart,
  SessionTurn,
} from '../types';
import {
  AssistantItem,
  ConversationState,
  MessageView,
  SteerState,
  TurnArtifacts,
  baseName,
  newID,
  normalizeArgs,
} from './model';
import { normalizeTurnStatus } from './artifacts';
import { capTextItem, capToolResult } from './stream';

// HISTORY_PAGE_TURNS / INITIAL_HISTORY_TURNS bound how much of a long
// conversation is pulled from the archive at once. Startup hydration
// takes the newest page only; scrolling above it pages backwards. Both
// requests ask for one turn more than they keep, so the extra turn can
// be dropped while still knowing whether older turns exist.
export const INITIAL_HISTORY_TURNS = 6;

export const HISTORY_PAGE_TURNS = 10;

// TAIL_SYNC_TURNS bounds one read of the transcript's tail: the turns
// appended after the newest seq the transcript holds are normally one
// app-authored turn (a delegation note), so the cap only exists to keep
// a pathological append burst from crossing the bridge in one payload.
export const TAIL_SYNC_TURNS = 20;

// historyPartsToAttachments extracts media parts from an archived user
// message into renderable attachments. The archive keeps URL-form
// sources (local paths under the session's media/files dirs).
export function historyPartsToAttachments(
  parts: HistoryPart[],
): AttachmentView[] {
  const out: AttachmentView[] = [];
  for (const p of parts) {
    if (p.type === 'image' && p.source?.kind === 'url' && p.source.url) {
      out.push({
        id: newID('att'),
        kind: 'image',
        path: p.source.url,
        name: baseName(p.source.url),
        media_type: p.source.media_type,
      });
    } else if (p.type === 'audio' && p.source?.kind === 'url' && p.source.url) {
      out.push({
        id: newID('att'),
        kind: 'audio',
        path: p.source.url,
        name: baseName(p.source.url),
        media_type: p.source.media_type,
      });
    } else if (p.type === 'video' && p.source?.kind === 'url' && p.source.url) {
      out.push({
        id: newID('att'),
        kind: 'video',
        path: p.source.url,
        name: baseName(p.source.url),
        media_type: p.source.media_type,
      });
    } else if (p.type === 'file' && p.uri) {
      out.push({
        id: newID('att'),
        kind: 'file',
        path: p.uri,
        name: p.name || baseName(p.uri),
        media_type: p.media_type,
      });
    }
  }
  return out;
}

// historyToMessages converts one archived turn's stored flowcraft
// messages back into the live MessageView shape: user text, then
// assistant messages with the same ordered blocks (reasoning, tool
// calls, text) the stream produces. Text-bearing assistant replies keep
// their own row so a long history stays cheap to render; tool-only
// rounds (which produce no visible separator) are appended to the
// previous assistant row so resumed sessions do not show a stack of
// repeated tool group cards.
//
// A user row past the turn's first one is a steer the turn's boundary
// delivered: the only user messages a turn's archive holds are the ask
// that opened it plus whatever the steer node appended mid-turn (tools
// -> steer -> compact in the assistant graph), and a resume has to keep
// rendering those as interjections instead of as new turns. Two guards
// keep the inference honest: compaction summaries are user-role context
// rows, not the user speaking, and a boundary only appends after a tool
// result, so a steer always follows an assistant round. A batch the
// boundary merged into one message (several parts in one user message)
// comes back as one row: the archive keeps the merge, so the split is
// not recoverable here.
export const historyToMessages = (history: HistoryMessage[]): MessageView[] => {
  const messages: MessageView[] = [];
  let sawAsk = false;
  let sawAssistant = false;
  // byCallID indexes the tool calls seen so far so a tool_result is O(1)
  // to attach. Scanning the accumulated list (the old `.find`) made
  // resuming a session quadratic in its number of tool calls, which is
  // exactly the shape of a long turn's archive.
  const byCallID = new Map<
    string,
    Extract<AssistantItem, { kind: 'tool_call' }>
  >();
  for (const h of history) {
    const parts = h.content?.parts ?? [];
    if (h.role === 'user') {
      const text = parts
        .filter((p): p is { type: 'text'; text?: string } => p.type === 'text')
        .map((p) => p.text ?? '')
        .join('');
      let steer: SteerState | undefined;
      if (!text.startsWith(COMPACT_SUMMARY_PREFIX)) {
        if (!sawAsk) {
          sawAsk = true;
        } else if (sawAssistant) {
          steer = 'delivered';
        }
      }
      messages.push({
        id: newID('msg'),
        role: 'user',
        text,
        items: [],
        attachments: historyPartsToAttachments(parts),
        steer,
      });
      continue;
    }
    if (h.role === 'tool') {
      for (const p of parts) {
        if (p.type !== 'tool_result' || !p.result) continue;
        const item = byCallID.get(p.result.call_id);
        if (item) {
          item.tool.status = p.result.is_error ? 'error' : 'done';
          item.tool.result = capToolResult(
            sanitizeToolResult(toolResultText(p.result.content)),
          );
          const images = toolResultImages(p.result.content);
          if (images.length > 0) item.tool.images = images;
        }
      }
      continue;
    }
    sawAssistant = true;
    const hasVisibleText = parts.some(
      (p) => p.type === 'text' && Boolean((p as { text?: string }).text),
    );
    let msg = messages[messages.length - 1];
    if (!msg || msg.role !== 'assistant' || hasVisibleText) {
      msg = {
        id: newID('msg'),
        role: 'assistant',
        text: '',
        items: [],
        attachments: [],
      };
      messages.push(msg);
    }
    for (const p of parts) {
      switch (p.type) {
        case 'text':
          if (p.text) {
            msg.items.push(
              capTextItem({ kind: 'text', id: newID('part'), text: p.text }),
            );
          }
          break;
        case 'reasoning':
          if (p.text) {
            msg.items.push(
              capTextItem({
                kind: 'reasoning',
                id: newID('part'),
                text: p.text,
              }),
            );
          }
          break;
        case 'tool_call': {
          const call = p.call;
          if (!call) break;
          const item: Extract<AssistantItem, { kind: 'tool_call' }> = {
            kind: 'tool_call',
            id: newID('part'),
            tool: {
              id: call.id,
              name: call.name,
              args: normalizeArgs(call.arguments),
              status: 'running',
            },
          };
          msg.items.push(item);
          byCallID.set(call.id, item);
          break;
        }
      }
    }
  }
  return messages;
};

// historyTurnsToState rebuilds the transcript and per-turn artifact
// groups from the archived per-turn records, so resuming renders one
// artifact strip under each turn's messages.
export function historyTurnsToState(turns: SessionTurn[]): {
  messages: MessageView[];
  turnArtifacts: TurnArtifacts[];
} {
  const messages: MessageView[] = [];
  const turnArtifacts: TurnArtifacts[] = [];
  for (const turn of turns) {
    const start = messages.length;
    messages.push(...historyToMessages(turn.messages));
    // A turn the app wrote is marked on its rows: the transcript renders
    // the card from the archived fields, and nothing that asks what the
    // user said reads them as speech. The mark rides on every row of the
    // turn, and the decoded card on the one row that carries the note's
    // text (the note turn holds exactly one message).
    if (turn.kind) {
      for (let i = start; i < messages.length; i++) {
        messages[i].kind = turn.kind;
      }
      const first = messages[start];
      if (first && turn.delegation_note) first.note = turn.delegation_note;
    }
    turnArtifacts.push({
      id: `h-${turn.seq}`,
      start,
      seq: turn.seq,
      runID: turn.run_id,
      requestedAt: turn.requested_at || turn.at,
      startedAt: turn.started_at || turn.at,
      finishedAt: turn.finished_at || turn.at,
      durationMs: turn.duration_ms,
      status: normalizeTurnStatus(turn.status),
      error: turn.error,
      interruptCause: turn.interrupt_cause,
      errorKind: turn.error_kind,
      requestID: turn.request_id,
      responseID: turn.response_id,
      docs: (turn.artifacts ?? []).map((a) => ({
        path: a.path,
        bytes: a.bytes ?? 0,
      })),
    });
  }
  return { messages, turnArtifacts };
}

// historyPage turns one Session.Turns response into conversation state.
// The caller asks for one turn more than the page keeps: the extra turn
// is dropped here, and its presence is what tells us older history
// still exists on the backend.
export function historyPage(
  turns: SessionTurn[],
  pageSize: number,
): {
  messages: MessageView[];
  turnArtifacts: TurnArtifacts[];
  historySeq: number;
  historyHasMore: boolean;
} {
  const historyHasMore = turns.length > pageSize;
  const page = historyHasMore ? turns.slice(turns.length - pageSize) : turns;
  const { messages, turnArtifacts } = historyTurnsToState(page);
  return {
    messages,
    turnArtifacts,
    historySeq: page[0]?.seq ?? 0,
    historyHasMore,
  };
}

// newestArchivedSeq is the seq of the newest archived turn the
// transcript holds, and 0 when it holds none (a live-only transcript, or
// one whose archived turns were all trimmed — seq starts at 1).
export function newestArchivedSeq(conv: ConversationState): number {
  let newest = 0;
  for (const turn of conv.turnArtifacts) {
    if (turn.seq !== undefined && turn.seq > newest) newest = turn.seq;
  }
  return newest;
}

// foldArchivedTurns folds archive turns into a transcript that is
// already on screen. Placement is by seq — the archive's write order —
// so a note the app wrote while another turn was still running lands
// above that turn's rows, which is where the next full hydrate renders
// it: a turn's row is written when it ends, so its seq is newer than the
// note's. Turns already present are dropped by seq and by run id,
// because the live path may have reconciled the same turn while this
// read was in flight.
//
// Returns undefined when nothing was added, so the caller can skip a
// store write.
export function foldArchivedTurns(
  conv: ConversationState,
  fresh: SessionTurn[],
): ConversationState | undefined {
  const knownSeq = new Set<number>();
  const knownRuns = new Set<string>();
  for (const turn of conv.turnArtifacts) {
    if (turn.seq !== undefined) knownSeq.add(turn.seq);
    if (turn.runID) knownRuns.add(turn.runID);
  }
  let messages = conv.messages;
  let turnArtifacts = conv.turnArtifacts;
  let added = false;
  for (const turn of fresh) {
    if (knownSeq.has(turn.seq)) continue;
    if (turn.run_id && knownRuns.has(turn.run_id)) continue;
    const rebuilt = historyTurnsToState([turn]);
    const strip = rebuilt.turnArtifacts[0];
    if (!strip || rebuilt.messages.length === 0) continue;
    let idx = turnArtifacts.findIndex(
      (t) => t.seq !== undefined && t.seq > turn.seq,
    );
    if (idx < 0) idx = turnArtifacts.length;
    const at =
      idx < turnArtifacts.length ? turnArtifacts[idx].start : messages.length;
    const rows = rebuilt.messages.length;
    messages = [
      ...messages.slice(0, at),
      ...rebuilt.messages,
      ...messages.slice(at),
    ];
    turnArtifacts = [
      ...turnArtifacts.slice(0, idx),
      { ...strip, start: at },
      ...turnArtifacts.slice(idx).map((t) => ({ ...t, start: t.start + rows })),
    ];
    knownSeq.add(turn.seq);
    if (turn.run_id) knownRuns.add(turn.run_id);
    added = true;
  }
  if (!added) return undefined;
  return { ...conv, messages, turnArtifacts };
}
