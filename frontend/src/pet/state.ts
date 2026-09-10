// Wire types and pure mapping for the desktop pet surface. The Go side
// broadcasts pet:state snapshots; this module turns them into the view
// model the renderer styles against. Keep it dependency-free so the
// mapping stays trivially unit-testable.

export type PetPhase =
  'idle' | 'thinking' | 'tool' | 'answering' | 'asking' | 'done' | 'error';

export type PetDisposition = 'roam' | 'sleep' | 'work' | 'ask';

export interface PetStatePayload {
  agent_id?: string;
  phase: PetPhase;
  tool_name?: string;
  tool_category?: string;
  disposition: PetDisposition;
  interactive?: boolean;
  walking?: boolean;
  /** True while the pet is asleep (disposition sleep). */
  sleeping?: boolean;
  intent?: string;
  /** Grows only when the intent changes; the renderer fires one-shots
   *  on the transition so a repeated broadcast does not replay them. */
  intent_seq?: number;
  bubble?: string;
}

export interface PetView {
  phase: PetPhase;
  disposition: PetDisposition;
  toolName?: string;
  toolCategory?: string;
  interactive: boolean;
  walking?: boolean;
  sleeping?: boolean;
  intent?: string;
  intentSeq: number;
  bubble?: string;
}

// PetActivityDTO mirrors pet.PetActivity from the Go feed (used by the
// Subagent Dock to show what non-assistant agents are doing).
export interface PetActivityDTO {
  agent_id: string;
  conversation_id?: string;
  run_id?: string;
  phase: PetPhase;
  tool?: { name: string; category: string };
  severity: number;
  ts: string;
}

export interface PetDriveSnapshot {
  attention: number;
  energy: number;
  comfort: number;
}

export interface PetMindDebug {
  drives: PetDriveSnapshot;
  mood: string;
  stats: {
    poke_count: number;
    last_poke?: string;
    last_sulk?: string;
  };
  disposition: string;
  phase: PetPhase;
  walking: boolean;
}

export function toPetView(payload: PetStatePayload | null): PetView {
  if (!payload) {
    return {
      phase: 'idle',
      disposition: 'roam',
      interactive: false,
      walking: false,
      sleeping: false,
      intentSeq: 0,
    };
  }
  return {
    phase: payload.phase,
    disposition: payload.disposition,
    toolName: payload.tool_name,
    toolCategory: payload.tool_category,
    interactive: Boolean(payload.interactive),
    walking: Boolean(payload.walking),
    sleeping: Boolean(payload.sleeping),
    intent: payload.intent,
    intentSeq: payload.intent_seq ?? 0,
    bubble: payload.bubble,
  };
}
