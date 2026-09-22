// useFileMarks keeps one file's git change marks fresh for the viewer.
//
// The marks answer "what did HEAD's version of this file look like",
// which changes from three directions: the user editing in an external
// editor, the agent writing during a turn, and the user staging or
// committing in the Git panel. Events cover all three (turn_end,
// artifact for the file this tab shows, git_changed for UI writes);
// the slow poll is the backstop for everything that does not announce
// itself. Results are compared by value so an unchanged answer never
// re-renders the editor.
import { useCallback, useEffect, useRef, useState } from 'react';
import { Events } from '@wailsio/runtime';
import { api } from '../../lib/api';
import { marksSignature } from '../../lib/fileMarks';
import type { FileTab, GitFileMarks } from '../../lib/types';

// POLL_MS is the idle re-read interval. Deliberately the same as the
// Git panel's status poll: the two are never mounted at once.
const POLL_MS = 5000;
// COALESCE_MS merges the burst of triggers one agent turn produces
// (turn_end plus a run of artifact events) into one read.
const COALESCE_MS = 120;

/**
 * useFileMarks returns the marks of `tab`, or null when there is
 * nothing to draw: the file is not in the workspace, the switch is off,
 * the file is clean, or the workspace is not a repository.
 */
export function useFileMarks(
  tab: FileTab | null,
  enabled: boolean,
): GitFileMarks | null {
  const path = tab?.path ?? '';
  // The artifact event carries the path the agent's file tools used,
  // which is workspace-relative; the tab holds an absolute one.
  const rel = tab?.rel ?? '';
  const active =
    enabled && tab !== null && tab.root === 'workspace' && path !== '';
  const [marks, setMarks] = useState<GitFileMarks | null>(null);
  const signature = useRef('');
  const seq = useRef(0);
  const timer = useRef<number | null>(null);

  const read = useCallback(async () => {
    if (!active) return;
    const id = (seq.current += 1);
    try {
      const res = await api.gitFileMarks(path);
      if (id !== seq.current) return;
      const next = marksSignature(res);
      if (next === signature.current) return;
      signature.current = next;
      setMarks(res);
    } catch {
      // Marks are decoration: a failing read (a vanished file, a broken
      // git) drops them instead of surfacing an error in the viewer.
      if (id !== seq.current) return;
      signature.current = '';
      setMarks(null);
    }
  }, [active, path]);

  const schedule = useCallback(() => {
    if (timer.current !== null) return;
    timer.current = window.setTimeout(() => {
      timer.current = null;
      void read();
    }, COALESCE_MS);
  }, [read]);

  // Opening a file, or switching tabs, reads immediately and drops the
  // previous file's marks: showing them against the new document for a
  // frame would draw marks on the wrong lines.
  useEffect(() => {
    signature.current = '';
    seq.current += 1;
    setMarks(null);
    if (active) void read();
  }, [active, read]);

  useEffect(() => {
    if (!active) return;
    const off = Events.On('opencraft:ui', (e) => {
      const ev = e.data as
        { type?: string; data?: { path?: string } } | undefined;
      if (ev?.type === 'git_changed' || ev?.type === 'turn_end') {
        schedule();
        return;
      }
      // artifact fires per written file; only this tab's own file has to
      // re-read, or every write during a turn would re-run the query.
      if (ev?.type === 'artifact' && ev.data?.path === rel) schedule();
    });
    const wake = () => {
      if (!document.hidden) schedule();
    };
    window.addEventListener('focus', wake);
    document.addEventListener('visibilitychange', wake);
    const poll = window.setInterval(() => {
      if (document.hidden) return;
      void read();
    }, POLL_MS);
    return () => {
      off();
      window.removeEventListener('focus', wake);
      document.removeEventListener('visibilitychange', wake);
      window.clearInterval(poll);
    };
  }, [active, rel, read, schedule]);

  useEffect(
    () => () => {
      if (timer.current !== null) window.clearTimeout(timer.current);
    },
    [],
  );

  return marks;
}
