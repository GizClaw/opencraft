import { describe, expect, it, vi } from 'vitest';
import { SessionController, type SessionControllerDeps } from './controller';
import { StateRoot } from './root';
import type { SessionSnapshot } from '../lib/types';

const snapshot = (id: string): SessionSnapshot => ({
  session_id: id,
  mode: 'workspace',
  think: 'medium',
  model: '',
});

describe('SessionController', () => {
  it('serializes backend switches and applies only the latest request', async () => {
    const root = new StateRoot();
    let resolveOld!: () => void;
    const opened: string[] = [];
    const newChat = vi.fn(async () => snapshot('s-new'));
    const resume = vi.fn(
      () =>
        new Promise<SessionSnapshot>((resolve) => {
          resolveOld = () => resolve(snapshot('s-old'));
        }),
    );
    const controller = new SessionController(root, {
      newChat,
      resume,
      onSessionOpened: (s) => opened.push(s.session_id),
    });

    const oldResume = controller.resume('s-old');
    const newChatPromise = controller.newChat();

    await vi.waitFor(() => expect(resume).toHaveBeenCalled());
    expect(newChat).not.toHaveBeenCalled();
    resolveOld();
    await oldResume;
    await newChatPromise;

    expect(root.focusSnapshot.value).toBe('active');
    expect(root.focusSnapshot.context.sessionID).toBe('s-new');
    expect(opened).toEqual(['s-new']);
  });

  it('does not mark focus failed when an older queued call fails', async () => {
    const root = new StateRoot();
    const opened: string[] = [];
    const newChat = vi.fn(async () => snapshot('s-new'));
    const resume = vi.fn(() => Promise.reject(new Error('old failed')));
    const controller = new SessionController(root, {
      newChat,
      resume,
      onSessionOpened: (s) => opened.push(s.session_id),
    });

    const oldResume = controller.resume('s-old').catch(() => undefined);
    await controller.newChat();
    await oldResume;

    expect(root.focusSnapshot.value).toBe('active');
    expect(root.focusSnapshot.context.sessionID).toBe('s-new');
    expect(opened).toEqual(['s-new']);
  });

  it('clicking the active session does not call the backend', async () => {
    const root = new StateRoot();
    const opened: string[] = [];
    const newChat = vi.fn(async () => snapshot('s-new'));
    const resume = vi.fn(async () => snapshot('s-1'));
    const controller = new SessionController(root, {
      newChat,
      resume,
      onSessionOpened: (s) => opened.push(s.session_id),
    });

    await controller.resume('s-1');
    await controller.resume('s-1');

    expect(resume).toHaveBeenCalledTimes(1);
  });
});
