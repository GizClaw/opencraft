import {
  createConversationActor,
  type ConversationInput,
} from './machines/conversation';

export type ConversationActor = ReturnType<typeof createConversationActor>;

export interface ConversationActorOptions {
  workspaceGeneration: number;
  readyEmpty?: boolean;
  workspace?: string;
}

/**
 * ConversationRegistry owns every per-conversation actor, the tombstone
 * set and the run→conversation index. Tombstones outlive actor
 * instances: an actor may be stopped early, but the id stays blocked
 * until resetWorkspace clears the whole registry.
 *
 * Run attribution lives here because which conversation a run belongs
 * to is control-plane state, and the actor is what says whether that run
 * is still alive: one table, next to the thing that can invalidate it,
 * and dropped together with the conversation.
 */
export class ConversationRegistry {
  private actors = new Map<string, ConversationActor>();
  private tombstones = new Set<string>();
  private runs = new Map<string, string>();
  private listeners = new Set<() => void>();
  private actorUnsubs = new Map<string, { unsubscribe: () => void }>();

  subscribe(listener: () => void): () => void {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  }

  private emit() {
    for (const listener of this.listeners) listener();
  }

  isDeleted(conversationID: string): boolean {
    return this.tombstones.has(conversationID);
  }

  get(conversationID: string): ConversationActor | undefined {
    return this.actors.get(conversationID);
  }

  /**
   * bindRun records that a run belongs to a conversation. Three paths
   * turn a run id into routing: the turn this window started, a run it
   * adopted off the wire, and an automation run.
   */
  bindRun(runID: string, conversationID: string): void {
    if (!runID || !conversationID) return;
    // A deleted conversation does not take on runs again: there is no
    // transcript left for one to settle in.
    if (this.tombstones.has(conversationID)) return;
    if (this.runs.get(runID) === conversationID) return;
    this.runs.set(runID, conversationID);
    this.emit();
  }

  /**
   * releaseRun forgets one run, which is what its terminal event does:
   * from there the transcript already holds everything the run reports.
   */
  releaseRun(runID: string): void {
    if (!this.runs.delete(runID)) return;
    this.emit();
  }

  /** conversationForRun answers which conversation owns a run, if any. */
  conversationForRun(runID: string): string | undefined {
    return this.runs.get(runID);
  }

  /** hasRun reports whether a conversation owns a run that has not ended. */
  hasRun(conversationID: string): boolean {
    for (const owner of this.runs.values()) {
      if (owner === conversationID) return true;
    }
    return false;
  }

  private dropRunsOf(conversationID: string): boolean {
    let dropped = false;
    for (const [runID, owner] of this.runs) {
      if (owner !== conversationID) continue;
      this.runs.delete(runID);
      dropped = true;
    }
    return dropped;
  }

  ensure(
    conversationID: string,
    options: ConversationActorOptions,
  ): ConversationActor | undefined {
    if (this.tombstones.has(conversationID)) {
      return undefined;
    }
    const existing = this.actors.get(conversationID);
    if (existing) {
      return existing;
    }
    const input: ConversationInput = {
      conversationID,
      workspaceGeneration: options.workspaceGeneration,
      readyEmpty: options.readyEmpty ?? false,
      workspace: options.workspace,
    };
    const actor = createConversationActor(input);
    this.actors.set(conversationID, actor);
    this.actorUnsubs.set(
      conversationID,
      actor.subscribe(() => this.emit()),
    );
    this.emit();
    return actor;
  }

  markDeleted(conversationID: string): void {
    this.tombstones.add(conversationID);
    // Deletion is terminal for the conversation's runs too: a deleted
    // chat never ends its turn, and a late event for one of its runs
    // must not route back into the id.
    this.dropRunsOf(conversationID);
    this.emit();
  }

  /**
   * release stops and removes one idle conversation actor without
   * tombstoning its id. The host store calls this when a finished
   * background conversation is evicted, so re-opening the conversation
   * hydrates through the normal transcript loading path again.
   */
  release(conversationID: string): void {
    const actor = this.actors.get(conversationID);
    if (!actor) return;
    this.actorUnsubs.get(conversationID)?.unsubscribe();
    actor.stop();
    this.actors.delete(conversationID);
    this.actorUnsubs.delete(conversationID);
    this.emit();
  }

  resetWorkspace(): void {
    for (const actor of this.actors.values()) {
      actor.stop();
    }
    // The runs belonged to the conversations of the workspace that is
    // going away; the next generation starts with an empty index.
    this.runs.clear();
    for (const subscription of this.actorUnsubs.values()) {
      subscription.unsubscribe();
    }
    this.actors.clear();
    this.actorUnsubs.clear();
    this.tombstones.clear();
    this.emit();
  }

  size(): number {
    return this.actors.size;
  }

  all(): ConversationActor[] {
    return [...this.actors.values()];
  }
}
