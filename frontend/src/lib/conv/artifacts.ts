import {
  ConversationState,
  TurnArtifacts,
  TurnDoc,
  TurnStatus,
  newTurnID,
} from './model';

// normalizeTurnStatus is the data layer's reading of a status string, and
// it only names what it can spell: an unrecognised status becomes
// undefined, and every caller keeps what the turn already says instead of
// guessing (writer.ts for a live turn, history.ts for an archived one).
// The router reads the same field differently on purpose - the turn
// machine must reach a terminal phase, so anything it does not recognise
// ends the turn as a failure (turnEndStatus in state/eventRouter.ts).
export function normalizeTurnStatus(status?: string): TurnStatus | undefined {
  switch (status) {
    case 'completed':
    case 'failed':
    case 'aborted':
    case 'canceled':
    case 'interrupted':
      return status;
    default:
      return undefined;
  }
}

// mergeTurnDoc appends a produced file, or refreshes its byte count in
// place when the same path is written again.
export function mergeTurnDoc(
  docs: TurnDoc[],
  path: string,
  bytes: number,
): TurnDoc[] {
  const idx = docs.findIndex((d) => d.path === path);
  const entry: TurnDoc = { path, bytes };
  if (idx < 0) return [...docs, entry];
  return [...docs.slice(0, idx), entry, ...docs.slice(idx + 1)];
}

// artifactTurnIndex finds the turn a run's events belong to. The backend
// attributes everything a run produced to that run, so the strip is
// found by run id: while another turn sits above the running one (a
// delegation note the app appended mid-turn, a barge-in replacement),
// the last strip is not the writer.
//
// A run id the store has not recorded yet means the start-turn response
// for that run is still in flight, so the file belongs to the trailing
// live entry — the one with neither a run id nor an archived seq. With
// no owner to point at, an artifact event is dropped rather than merged
// into whatever turn happens to be last; a stream delta opens the
// missing strip instead (see liveRunTurn).
//
// A strip that already finished is not a live entry, whatever it is
// missing: a send that never started leaves its turn stamped but
// unnamed (markFailedSend), and a run met on the wire must not fold into
// that turn — nor a file land on it.
export function artifactTurnIndex(
  list: TurnArtifacts[],
  runID: string | undefined,
): number {
  if (runID) {
    const idx = list.findIndex((t) => t.runID === runID);
    if (idx >= 0) return idx;
  }
  const live = list.length - 1;
  const last = list[live];
  if (!last || last.runID || last.seq !== undefined || last.finishedAt) {
    return -1;
  }
  return live;
}

// liveTurnEntry is the strip of the run the window is tracking, opened
// where the archive ends. Its start is an index into the merged
// transcript, and the merge appends the shell's rows whole
// ([...archive, ...shell]): a strip the shell already had keeps its rows
// by shifting with them (seam + start) rather than snapping to the seam.
// The run that opened it is not always the oldest one the shell holds —
// a barge-in replaces a run without erasing its rows — and a strip that
// snapped back would swallow rows that are not its own. Such a strip is
// grown instead of replaced too: its id, the files it already collected
// and the stamp it was requested at survive the re-hydrate. The strip
// names the run the actor is tracking; while a send's response is still
// in flight the window has none to name, and that send's continuation
// (beginTurn) names the newest strip when the answer lands, which is
// this one.
export function liveTurnEntry(
  seam: number,
  previous: TurnArtifacts | undefined,
  runID: string | undefined,
): TurnArtifacts {
  const carried =
    previous && previous.seq === undefined && !previous.finishedAt
      ? previous
      : undefined;
  return {
    id: carried?.id ?? newTurnID(),
    start: carried ? seam + carried.start : seam,
    docs: carried ? [...carried.docs] : [],
    requestedAt: carried?.requestedAt,
    runID: runID ?? carried?.runID,
  };
}

// liveRunTurn returns the strip that owns one run's rows, opening it at
// the end of the transcript when the conversation holds none. A run can
// outlive the window that watches it — the app was restarted, or the
// session was opened while its run was already going — and the
// conversation machine then adopts the run off the wire (the first
// stream of an idle conversation starts it). No send of this window
// opened that run a turn, so nothing owns its rows: its first text delta
// would fold into the last archived answer (lastAssistant grows the
// trailing row) and its files would be attributed to the turn above it.
// The strip starts where the transcript ends, which is where the run's
// first row lands, and it names the run, which is how the artifact,
// turn_end and archive-reconcile paths find it (artifactTurnIndex).
// Everything above that seam belongs to the archive or to another run's
// turn. `adopted` reports that the strip was opened here, which is the
// caller's cue to bind the run in the registry: routing a delta back to
// its conversation goes through that index, and a run this window never
// sent has no other way into it.
export function liveRunTurn(
  conv: ConversationState,
  runID: string,
): { turnArtifacts: TurnArtifacts[]; turn: TurnArtifacts; adopted: boolean } {
  const list = conv.turnArtifacts;
  const owner = artifactTurnIndex(list, runID);
  if (owner >= 0) {
    return { turnArtifacts: list, turn: list[owner], adopted: false };
  }
  const turn = liveTurnEntry(conv.messages.length, undefined, runID);
  return { turnArtifacts: [...list, turn], turn, adopted: true };
}
