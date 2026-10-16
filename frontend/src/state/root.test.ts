import { describe, expect, it } from 'vitest';
import { StateRoot } from './root';

describe('StateRoot', () => {
  it('owns focus plus a conversation registry per workspace', () => {
    const root = new StateRoot();
    const actor = root.registry.ensure('s-1', {
      workspaceGeneration: root.generation(),
      workspace: '/tmp/w',
    });
    expect(actor).toBeDefined();
    expect(root.registry.size()).toBe(1);
    expect(root.registry.isDeleted('s-1')).toBe(false);
    expect(actor?.getSnapshot().context.workspace).toBe('/tmp/w');
  });

  it('workspace reset stops conversations, clears tombstones, bumps generation', () => {
    const root = new StateRoot();
    root.registry.ensure('s-1', { workspaceGeneration: root.generation() });
    root.registry.markDeleted('s-1');

    const before = root.generation();
    root.resetWorkspace();

    expect(root.generation()).toBe(before + 1);
    expect(root.registry.size()).toBe(0);
    expect(root.registry.isDeleted('s-1')).toBe(false);
    expect(root.focusSnapshot.value).toBe('no-session');
  });

  it('answers which conversation owns a run, and forgets it on release', () => {
    const root = new StateRoot();
    root.registry.bindRun('r-1', 's-1');
    root.registry.bindRun('', 's-1');

    expect(root.registry.conversationForRun('r-1')).toBe('s-1');
    expect(root.registry.hasRun('s-1')).toBe(true);
    expect(root.registry.hasRun('s-2')).toBe(false);

    root.registry.releaseRun('r-1');

    expect(root.registry.conversationForRun('r-1')).toBeUndefined();
    expect(root.registry.hasRun('s-1')).toBe(false);
  });

  it('drops a conversation\u2019s runs with the id: delete and workspace reset', () => {
    const root = new StateRoot();
    root.registry.bindRun('r-1', 's-1');
    root.registry.bindRun('r-2', 's-2');

    root.registry.markDeleted('s-1');
    // A tombstoned id takes on no further runs either: a late event for
    // one of its runs has no transcript left to land in.
    root.registry.bindRun('r-3', 's-1');

    expect(root.registry.conversationForRun('r-1')).toBeUndefined();
    expect(root.registry.conversationForRun('r-3')).toBeUndefined();
    expect(root.registry.conversationForRun('r-2')).toBe('s-2');

    root.resetWorkspace();

    expect(root.registry.conversationForRun('r-2')).toBeUndefined();
  });

  it('release removes one idle conversation without tombstoning it', () => {
    const root = new StateRoot();
    const actor = root.registry.ensure('s-1', {
      workspaceGeneration: root.generation(),
    });
    root.registry.release('s-1');

    expect(root.registry.size()).toBe(0);
    expect(root.registry.isDeleted('s-1')).toBe(false);

    const recreated = root.registry.ensure('s-1', {
      workspaceGeneration: root.generation(),
    });
    expect(recreated).not.toBe(actor);
    expect(root.registry.size()).toBe(1);
  });
});
