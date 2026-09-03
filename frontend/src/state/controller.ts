import type { StateRoot } from './root';
import type { SessionSnapshot } from '../lib/types';

export interface SessionBackend {
  newChat(): Promise<SessionSnapshot>;
  resume(id: string): Promise<SessionSnapshot>;
}

export interface SessionControllerDeps extends SessionBackend {
  onSessionOpened(snapshot: SessionSnapshot): void;
}

/**
 * SessionController serializes backend context switches in the same
 * order as the legacy store queue. The focus actor records the latest
 * user intent immediately; results are only applied when their request
 * is still current, but every queued call still runs so the backend
 * current-session ends at the last requested conversation.
 */
export class SessionController {
  private queue: Promise<void> = Promise.resolve();

  constructor(
    private root: StateRoot,
    private deps: SessionControllerDeps,
  ) {}

  async restore(sessionID: string): Promise<void> {
    this.root.sendFocus({ type: 'RESTORE_FOCUS', sessionID });
    this.deps.onSessionOpened({
      session_id: sessionID,
      mode: 'workspace',
      think: 'medium',
      model: '',
    });
  }

  async newChat(): Promise<void> {
    this.root.sendFocus({ type: 'OPEN_NEW' });
    const request = this.root.focusSnapshot.context.request;
    try {
      const snapshot = await this.enqueue(() => this.deps.newChat());
      this.applyResult(request, snapshot);
    } catch (err) {
      this.applyFailure(request, String(err));
    }
  }

  async resume(id: string): Promise<void> {
    if (this.isActiveSession(id)) return;
    this.root.sendFocus({ type: 'OPEN_SESSION', id });
    const request = this.root.focusSnapshot.context.request;
    try {
      const snapshot = await this.enqueue(() => this.deps.resume(id));
      this.applyResult(request, snapshot);
    } catch (err) {
      this.applyFailure(request, String(err));
    }
  }

  backFromFailure(): void {
    this.root.sendFocus({ type: 'BACK' });
  }

  private isActiveSession(id: string): boolean {
    return (
      this.root.focusSnapshot.value === 'active' &&
      this.root.focusSnapshot.context.sessionID === id
    );
  }

  private applyResult(request: number, snapshot: SessionSnapshot): void {
    this.root.sendFocus({
      type: 'OPEN_SUCCEEDED',
      request,
      sessionID: snapshot.session_id,
    });
    const state = this.root.focusSnapshot;
    if (state.value === 'active' && state.context.sessionID === snapshot.session_id) {
      this.deps.onSessionOpened(snapshot);
    }
  }

  private applyFailure(request: number, error: string): void {
    this.root.sendFocus({ type: 'OPEN_FAILED', request, error });
  }

  private enqueue<T>(op: () => Promise<T>): Promise<T> {
    const next = this.queue.then(op);
    this.queue = next.then(
      () => undefined,
      () => undefined,
    );
    return next;
  }
}
