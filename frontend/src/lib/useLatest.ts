// The "newest request wins" guard.
//
// Half the viewer surfaces fetch on selection — a diff, a commit's file, a
// file search, a plugin path inspection — and nothing ordered those
// responses: the panel that asked second can answer first, and the older
// answer then lands on top of the newer selection (a diff body belonging to
// another file while the header names the file the user picked, a state chip
// that still says "open" for a merged PR, a spinner the stale `finally` ends
// early).
//
// It started as the pattern three components had hand-rolled side by side
// (`FilePreviewPane`'s `loadSeq`, `useFileMarks`'s `seq.current`,
// `PRDetailModal`'s `alive`); they now call this instead, and the guard is
// written once. It is deliberately not a data-fetching hook: the callers
// differ in *what* they own (one panel's detail, a spinner, a selection),
// and the one thing they share is the ticket.
//
// Unmount is part of the contract, not an afterthought. A response that
// arrives after the panel closed must not write state, and a `finally`
// that ends a spinner is only allowed to do so while it still owns the
// panel — every ticket taken before unmount goes stale at once. A *newer*
// request also retires the older ticket, which is why the older `finally`
// stays quiet instead of ending the newer load's spinner.

import { useEffect, useMemo, useRef } from 'react';

export interface LatestRequest {
  /** begin starts a request and returns its ticket. */
  begin(): number;
  /** current is the ticket of the newest request; 0 before the first. */
  current(): number;
  /** isCurrent reports whether a ticket may still write its result. */
  isCurrent(ticket: number): boolean;
  /**
   * retire invalidates every ticket issued so far without taking one. A
   * caller needs it on a *change of subject* — a tab switch, a different
   * pull request — where the answer in flight belongs to what the reader
   * left behind and nothing is going to ask for it again.
   */
  retire(): void;
}

export function useLatestRequest(): LatestRequest {
  const seq = useRef(0);
  const alive = useRef(true);
  useEffect(() => {
    // Set in the body as well as cleared in the cleanup: React runs
    // effects twice in StrictMode, and the second mount must be live.
    alive.current = true;
    return () => {
      alive.current = false;
      seq.current += 1;
    };
  }, []);
  return useMemo(
    () => ({
      begin: () => {
        seq.current += 1;
        return seq.current;
      },
      current: () => seq.current,
      isCurrent: (ticket: number) => alive.current && ticket === seq.current,
      retire: () => {
        seq.current += 1;
      },
    }),
    [],
  );
}
