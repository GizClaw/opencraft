import { describe, expect, it } from 'vitest';
import { StateRoot } from './root';

describe('StateRoot', () => {
  it('owns focus plus a conversation registry per workspace', () => {
    const root = new StateRoot();
    const actor = root.registry.ensure('s-1', {
      workspaceGeneration: root.generation(),
    });
    expect(actor).toBeDefined();
    expect(root.registry.size()).toBe(1);
    expect(root.registry.isDeleted('s-1')).toBe(false);
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
