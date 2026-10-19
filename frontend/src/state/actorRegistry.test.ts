// state/actorRegistry.ts: the registry owns every per-conversation actor,
// the tombstone set, the run→conversation index and the change fan-out
// into the root version. The cases here pin the parts root.test.ts does
// not reach: what counts as a change (the signature helpers upstream
// re-render on it), and what a tombstone keeps refusing.

import { describe, expect, it } from 'vitest';
import { ConversationRegistry } from './actorRegistry';
import { StateRoot } from './root';

function counting() {
  const registry = new ConversationRegistry();
  let notified = 0;
  registry.subscribe(() => {
    notified += 1;
  });
  return { registry, notified: () => notified };
}

describe('ConversationRegistry', () => {
  it('notifies on every change, and stops once unsubscribed', () => {
    const registry = new ConversationRegistry();
    let notified = 0;
    const unsubscribe = registry.subscribe(() => {
      notified += 1;
    });

    registry.ensure('s-1', { workspaceGeneration: 0 });
    expect(notified).toBeGreaterThan(0);

    const afterEnsure = notified;
    registry.bindRun('r-1', 's-1');
    expect(notified).toBe(afterEnsure + 1);

    unsubscribe();
    registry.bindRun('r-2', 's-1');
    expect(notified).toBe(afterEnsure + 1);
  });

  it('ignores a run with no id on either side, without notifying', () => {
    const { registry, notified } = counting();

    registry.bindRun('', 's-1');
    registry.bindRun('r-1', '');

    expect(notified()).toBe(0);
    expect(registry.conversationForRun('')).toBeUndefined();
    expect(registry.conversationForRun('r-1')).toBeUndefined();
  });

  it('treats a rebinding of the same pair as no change, a new owner as one', () => {
    const { registry, notified } = counting();
    registry.bindRun('r-1', 's-1');
    const bound = notified();

    registry.bindRun('r-1', 's-1');
    expect(notified()).toBe(bound);

    registry.bindRun('r-1', 's-2');
    expect(notified()).toBe(bound + 1);
    expect(registry.conversationForRun('r-1')).toBe('s-2');
    expect(registry.hasRun('s-1')).toBe(false);
    expect(registry.hasRun('s-2')).toBe(true);
  });

  it('releases a run once, and only when it had it', () => {
    const { registry, notified } = counting();
    registry.bindRun('r-1', 's-1');
    const bound = notified();

    registry.releaseRun('r-1');
    expect(notified()).toBe(bound + 1);

    registry.releaseRun('r-1');
    expect(notified()).toBe(bound + 1);
  });

  it('ensure returns the actor it already holds, without notifying', () => {
    const { registry, notified } = counting();
    const first = registry.ensure('s-1', { workspaceGeneration: 0 });
    const opened = notified();

    const second = registry.ensure('s-1', { workspaceGeneration: 0 });

    expect(second).toBe(first);
    expect(notified()).toBe(opened);
  });

  it('a tombstone outlives the actor: released or not, the id stays refused', () => {
    const { registry } = counting();
    registry.ensure('s-1', { workspaceGeneration: 0 });
    registry.ensure('s-2', { workspaceGeneration: 0 });

    registry.markDeleted('s-1');
    expect(registry.ensure('s-1', { workspaceGeneration: 0 })).toBeUndefined();

    // s-2 shows what a release alone does: the actor goes, the id stays
    // open. s-1 does not get that second chance.
    registry.release('s-2');
    registry.release('s-1');
    expect(registry.ensure('s-2', { workspaceGeneration: 0 })).toBeDefined();
    expect(registry.ensure('s-1', { workspaceGeneration: 0 })).toBeUndefined();
  });

  it('releasing an id it does not hold is not a change', () => {
    const { registry, notified } = counting();

    registry.release('never-opened');

    expect(notified()).toBe(0);
    expect(registry.size()).toBe(0);
  });

  it('forwards the input each actor opens with', () => {
    const { registry } = counting();

    const actor = registry.ensure('s-1', {
      workspaceGeneration: 3,
      readyEmpty: true,
      workspace: '/tmp/w',
    });

    const snapshot = actor?.getSnapshot();
    expect(snapshot?.value).toMatchObject({
      lifecycle: 'alive',
      transcript: 'ready',
    });
    expect(snapshot?.context.workspace).toBe('/tmp/w');
    expect(snapshot?.context.emptyTranscript).toBe(true);
    expect(snapshot?.context.workspaceGeneration).toBe(3);
  });

  it('carries a conversation transition up to the root version', () => {
    const root = new StateRoot();
    const unsubscribe = root.subscribe(() => {});
    const actor = root.registry.ensure('s-1', {
      workspaceGeneration: root.generation(),
    });
    const before = root.getVersion();

    actor?.send({ type: 'SUBMIT', kind: 'send' });
    actor?.send({ type: 'RUN_STARTED', runID: 'r-1' });

    expect(root.getVersion()).toBeGreaterThan(before);
    unsubscribe();
  });

  it('lists every actor it holds', () => {
    const { registry } = counting();
    expect(registry.all()).toEqual([]);

    registry.ensure('s-1', { workspaceGeneration: 0 });
    registry.ensure('s-2', { workspaceGeneration: 0 });

    expect(registry.all()).toHaveLength(2);
    expect(registry.size()).toBe(2);
    expect(registry.get('s-3')).toBeUndefined();
  });
});
