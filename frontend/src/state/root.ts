import { createActor } from 'xstate';
import { ConversationRegistry } from './actorRegistry';
import { sessionFocusMachine, type FocusEvent } from './machines/sessionFocus';

function makeFocusActor(sessionID: string) {
  return createActor(sessionFocusMachine, {
    input: { sessionID },
  });
}

/**
 * StateRoot owns the root focus actor plus the per-conversation actor
 * registry. Registry actors survive workspace switches so turns still
 * running in a previous workspace keep streaming; only the focus actor
 * is reset for the newly active workspace.
 */
export class StateRoot {
  readonly registry = new ConversationRegistry();
  private focusActor: ReturnType<typeof makeFocusActor>;
  private workspaceGeneration = 0;
  private version = 0;

  constructor(sessionID = '') {
    this.focusActor = makeFocusActor(sessionID);
    this.focusActor.start();
  }

  subscribe(listener: () => void): () => void {
    const wrapped = () => {
      this.version += 1;
      listener();
    };
    const unsubFocus = this.focusActor.subscribe(wrapped) as unknown as {
      unsubscribe(): void;
    };
    const unsubRegistry = this.registry.subscribe(wrapped);
    return () => {
      unsubFocus.unsubscribe();
      unsubRegistry();
    };
  }

  getVersion(): number {
    return this.version;
  }

  get focusSnapshot() {
    return this.focusActor.getSnapshot();
  }

  sendFocus(event: FocusEvent) {
    this.focusActor.send(event);
  }

  generation(): number {
    return this.workspaceGeneration;
  }

  /**
   * Resets workspace-scoped state: old conversations are stopped,
   * tombstones are cleared, and focus returns to no-session.
   */
  resetWorkspace() {
    this.workspaceGeneration += 1;
    this.registry.resetWorkspace();
    this.focusActor.send({ type: 'WORKSPACE_RESET' });
  }

  stop() {
    this.registry.resetWorkspace();
    this.focusActor.stop();
  }
}
