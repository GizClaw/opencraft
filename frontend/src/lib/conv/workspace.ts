import type { StoreApi } from 'zustand';
import type { StoreState } from '../store';
import { stateRoot } from '../../state/app';
import { api } from '../api';
import { reportFrontendError } from '../frontendErrors';
import { sameWorkspacePath, workspaceLabel } from '../workspacePath';

// Workspace switching, as the store's own glue sees it: the wait for a
// landed switch, the landing itself and the reset that follows one. S4
// moved these out of store.ts (move only); the actions that drive them
// (loadWorkspaces, openWorkspace, restoreWorkspaceSession, the draft
// send) stayed behind for the shell slice.

export function createWorkspaceGlue(store: StoreApi<StoreState>) {
  const set = store.setState;
  const get = store.getState;

  let workspaceRestoreInFlight = false;

  let workspaceRestorePromise: Promise<void> | null = null;

  // suppressRestoreFor skips the automatic session restore after one
  // workspace switch. The new-chat flow uses it while a first message
  // is being sent to a different workspace: no previous session should
  // flash on screen and no empty draft should be minted on the way.
  let suppressRestoreFor: string | null = null;

  const waitForWorkspaceRestore = async () => {
    const deadline = Date.now() + 5000;
    while (Date.now() < deadline) {
      if (!workspaceRestoreInFlight) return;
      if (workspaceRestorePromise) {
        await workspaceRestorePromise;
        return;
      }
      await new Promise((resolve) => setTimeout(resolve, 10));
    }
  };

  // applyWorkspaceSwitch lands the window on one workspace: the focus
  // resets, the active workspace moves, and the workspace's saved
  // session is restored. The ready event is the normal caller; a switch
  // whose event never arrived enters through the same door (see
  // switchWorkspace), so the two cannot drift apart.
  const applyWorkspaceSwitch = (workDir: string) => {
    // Keep conversation actors and transcripts alive across a
    // workspace switch: a turn that is still running in the old
    // workspace continues to stream into its conversation. Only the
    // focus is reset; the session restore below decides which
    // conversation the new workspace shows.
    stateRoot.sendFocus({ type: 'WORKSPACE_RESET' });
    set({
      workspace: workDir,
      toolsView: null,
      configOpen: false,
      paletteOpen: false,
      shortcutsOpen: false,
    });
    void get().loadSessions();
    const restoreFor = suppressRestoreFor;
    suppressRestoreFor = null;
    if (restoreFor !== workDir) {
      const restore = get().restoreWorkspaceSession(workDir);
      workspaceRestoreInFlight = true;
      workspaceRestorePromise = restore;
      void restore.finally(() => {
        if (workspaceRestorePromise === restore) {
          workspaceRestorePromise = null;
          workspaceRestoreInFlight = false;
        }
      });
    }
  };

  // switchWorkspace waits out one workspace switch and answers the path
  // the window is on: the path asked for when the switch landed (the
  // backend announces it through "ready", and Workspace.Open has
  // already stamped the new workdir by the time the binding answers),
  // the path it moved to instead when another switch has taken the
  // window since, or '' when nothing settled.
  //
  // The announcement is not guaranteed to arrive. The backend skips it
  // when the workspace asked for is the one it already announced, and
  // it compares canonical paths there while the window compares the
  // raw strings — a window holding the same directory under another
  // spelling would wait out the deadline and lose the click. So the
  // wait is two-phase: a short grace for the event, then the backend's
  // own answer (the binding has applied the switch by now, so its
  // Active workspace is authoritative) landed here. Landing it is
  // idempotent with a late event: the ready handler compares the
  // workspace and does nothing when it already matches.
  const switchWorkspace = async (
    workspacePath: string,
    timeoutMs: number,
  ): Promise<string> => {
    const startedAt = Date.now();
    const deadline = startedAt + timeoutMs;
    const grace = Math.min(250, timeoutMs);
    while (
      get().workspace !== workspacePath &&
      Date.now() - startedAt < grace
    ) {
      await new Promise((resolve) => setTimeout(resolve, 20));
    }
    if (get().workspace === workspacePath) return workspacePath;
    const active = await api.workspace().catch(() => '');
    const held = get().workspace;
    if (sameWorkspacePath(held, workspacePath)) {
      // The switch landed while the backend was answering: the window
      // already holds it, under whatever spelling, which is what this
      // call answers.
      return held;
    }
    if (active && !sameWorkspacePath(active, workspacePath)) {
      // The backend is on a workspace this click never asked for: a
      // newer switch owns the window now, whether or not the window has
      // already followed it there. Waiting for an announcement that
      // will never come would only delay a click that has lost.
      return active;
    }
    if (active) {
      // The backend is on the workspace this click asked for: land the
      // window on it.
      if (active !== held) applyWorkspaceSwitch(active);
      return active;
    }
    // The backend would not say where it is, and the window has not moved
    // either. The announcement is the only thing left that could settle
    // this.
    while (get().workspace !== workspacePath && Date.now() < deadline) {
      await new Promise((resolve) => setTimeout(resolve, 20));
    }
    if (get().workspace === workspacePath) return workspacePath;
    // Nothing settled. Every caller turns this into an error the reader
    // sees once and the click path catches, so the log would never hear
    // about a missed switch; report it here, where both strings are
    // known, so the next one can be diagnosed from the log the way this
    // one could not.
    reportFrontendError(
      'workspace-switch',
      new Error('workspace switch did not complete'),
      `wanted ${workspaceLabel(workspacePath)}, active ${workspaceLabel(get().workspace)}`,
    );
    return '';
  };

  // The draft send pins and then clears that window from outside.
  const suppressRestore = (workDir: string | null) => {
    suppressRestoreFor = workDir;
  };

  return {
    workspaceRestoreInFlight,
    workspaceRestorePromise,
    suppressRestoreFor,
    waitForWorkspaceRestore,
    applyWorkspaceSwitch,
    switchWorkspace,
    suppressRestore,
  };
}
