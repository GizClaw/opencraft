import type { StoreApi } from 'zustand';
import { activeConversationID } from '../conv/core';
import { api } from '../api';
import { createConvCore } from '../conv/core';
import { followLinkTarget } from '../linkTarget';
import { viewerDefaults } from '../conv/model';
import type { StoreState } from '../store';

// The file viewers: tabs, the git pick handoff, diff panels and the
// tools drawer. S4 moved these out of store.ts (move only).

export function createShellViewers(
  store: StoreApi<StoreState>,
  core: ReturnType<typeof createConvCore>,
): Pick<
  StoreState,
  | 'openTools'
  | 'closeTools'
  | 'openFiles'
  | 'closeFiles'
  | 'openFileTarget'
  | 'openResolvedTarget'
  | 'closeFileTab'
  | 'activateFileTab'
  | 'setPanelMode'
  | 'focusGitPath'
  | 'consumeGitPick'
  | 'showFileDir'
> {
  const set = store.setState;
  const get = store.getState;
  const { viewerPatch } = core;

  // gitPickNonce numbers the viewer → Git panel handoffs. A counter (not
  // a timestamp) so two clicks in the same millisecond stay distinct.
  let gitPickNonce = 0;

  return {
    openTools: (view) => set({ toolsView: view, configOpen: false }),

    closeTools: () => set({ toolsView: null }),

    openFiles: () => viewerPatch(activeConversationID(), { filesOpen: true }),

    closeFiles: () => viewerPatch(activeConversationID(), { filesOpen: false }),

    openFileTarget: (target, base = '') =>
      followLinkTarget(target, base, {
        openFile: (res) => get().openResolvedTarget(res),
        openDir: (res) => {
          if (res.root !== 'workspace') return api.revealArtifact(res.path);
          viewerPatch(activeConversationID(), {
            filesOpen: true,
            fileTreeDir: res.rel || '.',
          });
        },
        onError: (message) => get().flash(message),
      }),

    openResolvedTarget: (res) =>
      set((state) => {
        const id = activeConversationID();
        if (!id) return {};
        const viewer = state.viewers[id] ?? viewerDefaults();
        // Placeholder tabs are transient: picking a real file from the
        // tree replaces the active blank tab instead of stacking next
        // to it. Other placeholder tabs (if any) stay untouched.
        const tabs = viewer.fileTabs.filter(
          (t) =>
            t.key !== res.path &&
            !(t.path === '' && t.key === viewer.fileActive),
        );
        tabs.push({
          key: res.path,
          path: res.path,
          rel: res.rel,
          root: res.root,
          name:
            res.name ||
            (res.rel ? (res.rel.split('/').pop() ?? res.rel) : res.path),
          media_type: res.media_type ?? '',
        });
        return {
          viewers: {
            ...state.viewers,
            [id]: {
              ...viewer,
              filesOpen: true,
              panelMode: 'files',
              fileTabs: tabs,
              fileActive: res.path,
            },
          },
        };
      }),

    closeFileTab: (key) =>
      set((state) => {
        const id = activeConversationID();
        if (!id) return {};
        const viewer = state.viewers[id] ?? viewerDefaults();
        const tabs = viewer.fileTabs.filter((t) => t.key !== key);
        const active =
          viewer.fileActive === key
            ? (tabs[tabs.length - 1]?.key ?? null)
            : viewer.fileActive;
        return {
          viewers: {
            ...state.viewers,
            [id]: { ...viewer, fileTabs: tabs, fileActive: active },
          },
        };
      }),

    activateFileTab: (key) =>
      viewerPatch(activeConversationID(), { fileActive: key }),

    setPanelMode: (mode) =>
      viewerPatch(activeConversationID(), { panelMode: mode }),

    focusGitPath: (path) =>
      viewerPatch(activeConversationID(), {
        panelMode: 'git',
        gitPick: { path, nonce: (gitPickNonce += 1) },
      }),

    consumeGitPick: () =>
      viewerPatch(activeConversationID(), { gitPick: undefined }),

    showFileDir: (rel) =>
      viewerPatch(activeConversationID(), {
        fileTreeDir: rel || '.',
        filesOpen: true,
      }),
  };
}
