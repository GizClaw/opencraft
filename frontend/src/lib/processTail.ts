import {
  useEffect,
  useRef,
  useState,
  type Dispatch,
  type SetStateAction,
} from 'react';
import { api } from './api';
import type { SandboxProcess } from './types';

// PROCESS_POLL_MS paces the feed while the conversation runs a turn: a
// live tail is what the section is for, and each answer is a snapshot
// of bounded tails rather than a log. Polling beats a stream
// subscription here — no event wiring, and a dropped poll just keeps
// the previous snapshot.
const PROCESS_POLL_MS = 1500;
// PROCESS_IDLE_POLL_MS paces the same feed while no turn runs but a
// process still does: an exec_session server outlives the turn that
// started it, and its output is then a slow-moving log.
const PROCESS_IDLE_POLL_MS = 5000;

// EMPTY keeps "another conversation" reference-stable, so rows that do
// not belong to the open conversation never re-render its callers.
const EMPTY: SandboxProcess[] = [];

// processKey is the identity of everything a read can change: output
// (seq), liveness, and the exit status. An unchanged row keeps its
// identity, so the card does not re-render (and a scrolled tail stays
// put) when nothing moved.
const processKey = (p: SandboxProcess) =>
  `${p.process_id}|${p.seq}|${p.running}|${p.exit_code ?? ''}|${
    p.truncated ? '1' : '0'
  }`;

const sameProcesses = (a: SandboxProcess[], b: SandboxProcess[]) =>
  a.length === b.length &&
  a.every((p, i) => processKey(p) === processKey(b[i]));

interface Snapshot {
  // id is the conversation the rows came from: switching conversations
  // must never show the previous session's processes while the first
  // read is in flight.
  id: string;
  rows: SandboxProcess[];
}

async function readSnapshot(
  id: string,
  set: Dispatch<SetStateAction<Snapshot>>,
): Promise<void> {
  try {
    const rows = (await api.processes(id)) ?? [];
    set((prev) =>
      prev.id === id && sameProcesses(prev.rows, rows) ? prev : { id, rows },
    );
  } catch {
    // A transient backend miss keeps the previous snapshot; the next
    // read replaces it.
  }
}

/**
 * useProcessTail returns the conversation's sandboxed child processes
 * with their output tails, refreshed while that is worth doing: the
 * conversation reads them once when it opens, then polls while it runs
 * a turn, and keeps polling while one of its processes still runs (a
 * server started by exec_session outlives the turn). Once neither is
 * true, the last poll is the last read.
 *
 * The feed is a per-generation resource, so a runtime reload (a
 * settings save, a plugin install) answers with an empty list: the
 * generation that started those processes is gone, and so are they.
 *
 * The reads are: one when the conversation opens, one at the moment a
 * turn ends, and then the poll while a turn runs or a process still
 * does. That middle read is what keeps the last poll gap honest — a
 * session started after the turn's final poll is found there, because
 * the sandbox registers a session's row when it starts, and a turn
 * cannot end before its own tool call has returned.
 */
export function useProcessTail(
  conversationID: string,
  turnRunning: boolean,
): SandboxProcess[] {
  const [snapshot, setSnapshot] = useState<Snapshot>({ id: '', rows: EMPTY });
  const rows = snapshot.id === conversationID ? snapshot.rows : EMPTY;
  const watching =
    conversationID !== '' && (turnRunning || rows.some((p) => p.running));
  const lastTurn = useRef(false);

  // One read per conversation: opening a session shows the last
  // command's output without a timer behind it.
  useEffect(() => {
    if (conversationID === '') return;
    void readSnapshot(conversationID, setSnapshot);
  }, [conversationID]);

  // The turn's last read. Everything below exists only while a turn
  // runs or a process is already known to run, so a process that starts
  // inside the final poll gap would otherwise be missed for good: no
  // read follows, the card that follows this hook leaves with the turn,
  // and the server stays invisible until the next message. A running
  // answer here hands the conversation to the idle pace below.
  useEffect(() => {
    const wasRunning = lastTurn.current;
    lastTurn.current = turnRunning;
    if (turnRunning || !wasRunning || conversationID === '') return;
    void readSnapshot(conversationID, setSnapshot);
  }, [conversationID, turnRunning]);

  // The repeating read. It exists only while there is something to
  // watch, so an idle conversation costs nothing; the read that reports
  // the last process stopped is also the one that ends it.
  useEffect(() => {
    if (!watching) return;
    const timer = window.setInterval(
      () => void readSnapshot(conversationID, setSnapshot),
      turnRunning ? PROCESS_POLL_MS : PROCESS_IDLE_POLL_MS,
    );
    return () => window.clearInterval(timer);
  }, [conversationID, turnRunning, watching]);

  return rows;
}
