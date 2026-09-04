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
 * ConversationRegistry owns every per-conversation actor and the
 * tombstone set. Tombstones outlive actor instances: an actor may be
 * stopped early, but the id stays blocked until resetWorkspace clears
 * the whole registry.
 */
export class ConversationRegistry {
  private actors = new Map<string, ConversationActor>();
  private tombstones = new Set<string>();
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
