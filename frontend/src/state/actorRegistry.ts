import {
  createConversationActor,
  type ConversationInput,
} from './machines/conversation';

export type ConversationActor = ReturnType<typeof createConversationActor>;

export interface ConversationActorOptions {
  workspaceGeneration: number;
  readyEmpty?: boolean;
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
    };
    const actor = createConversationActor(input);
    this.actors.set(conversationID, actor);
    return actor;
  }

  markDeleted(conversationID: string): void {
    this.tombstones.add(conversationID);
  }

  resetWorkspace(): void {
    for (const actor of this.actors.values()) {
      actor.stop();
    }
    this.actors.clear();
    this.tombstones.clear();
  }

  size(): number {
    return this.actors.size;
  }
}
