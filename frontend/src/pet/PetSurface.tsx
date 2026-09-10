import { useEffect, useRef, useState } from 'react';
import type * as React from 'react';
import { Events } from '@wailsio/runtime';
import { useTranslation } from 'react-i18next';
import { api } from '../lib/api';
import type { PetPack } from './pack';
import { pickPack } from './pack';
import { base64ToArrayBuffer, createPetRive, type PetRiveHandle } from './rive';
import { toPetView, type PetStatePayload, type PetView } from './state';
import type { PetRuntimeStatus } from './validate';
import './pet.css';

interface PendingRivePack {
  pack: PetPack;
  bytes: ArrayBuffer;
}

// unmountedStatus builds the report for a character whose asset never
// mounted: no asset named, the bytes could not be fetched, or the
// runtime failed to load them.
function unmountedStatus(pack: PetPack, error: string): PetRuntimeStatus {
  return {
    pack_id: pack.id,
    artboard: pack.artboard,
    state_machine: pack.stateMachine,
    view_model: pack.viewModel,
    ok: false,
    error,
  };
}

// reportRuntimeStatus hands one mount report to the backend, which
// keeps the last one for the settings diagnostics panel. Reporting is
// best effort: a failure here must never break the surface.
function reportRuntimeStatus(status: PetRuntimeStatus) {
  void api.petReportRuntimeStatus(status).catch((err) => {
    console.warn('pet: runtime status report failed', err);
  });
}

// petIdlePauseAfter is how long the surface keeps rendering after the
// last state change before parking the runtime. Rive drives its own
// rAF loop, so pausing is what stops the CPU cost of a still frame.
const petIdlePauseAfter = 20_000;

/**
 * PetSurface is the whole-screen roaming pet renderer mounted by the
 * pet Wails window (?surface=pet). It is deliberately inert: no plugin
 * host, no main store. The Go PetDirector broadcasts pet:state
 * snapshots; the surface resolves the active pack, mounts the Rive
 * state machine and view model on a canvas, and drives it with those
 * snapshots.
 */
export default function PetSurface() {
  const { t } = useTranslation();
  const [pet, setPet] = useState<PetView>(() => toPetView(null));
  const [riveReady, setRiveReady] = useState(false);
  const [pending, setPending] = useState<PendingRivePack | null>(null);
  const [activePack, setActivePack] = useState<PetPack | null>(null);
  const [status, setStatus] = useState<PetRuntimeStatus | null>(null);
  const [mountError, setMountError] = useState<string | null>(null);
  const [reloadToken, setReloadToken] = useState(0);
  const [reaction, setReaction] = useState<{
    intent?: string;
    bubble?: string;
  }>({});
  const reactionTimer = useRef<number | undefined>(undefined);
  const pauseTimer = useRef<number | undefined>(undefined);
  const hiddenRef = useRef(false);
  const lastIntentSeqRef = useRef(0);
  const canvasRef = useRef<HTMLCanvasElement | null>(null);
  const riveRef = useRef<PetRiveHandle | null>(null);
  // Latest surface state for the rover-independent readers (the idle
  // park timer and the mount path). It is written where the state is
  // produced, not during render, so two payloads delivered in the same
  // batch cannot both compare against the same stale value.
  const petRef = useRef(pet);
  // Drag state: start point + accumulated pointer position so a tiny
  // click is distinguishable from a drag.
  const dragRef = useRef({
    active: false,
    startScreenX: 0,
    startScreenY: 0,
    startWin: null as { x: number; y: number } | null,
    desired: null as { x: number; y: number } | null,
    moved: 0,
    inFlight: false,
    raf: 0,
  });

  // Park the runtime once the character has been still for a while:
  // sleeping or idling with no motion, no intent and no fresh payload.
  const scheduleIdlePause = () => {
    if (pauseTimer.current !== undefined) {
      window.clearTimeout(pauseTimer.current);
    }
    pauseTimer.current = window.setTimeout(() => {
      pauseTimer.current = undefined;
      const view = petRef.current;
      const idle = view.phase === 'idle' && !view.intent;
      if (view.walking) {
        // Still roaming: the walk cycle has to keep playing.
        scheduleIdlePause();
        return;
      }
      if (!idle) return;
      riveRef.current?.pause();
    }, petIdlePauseAfter);
  };

  // Show one reaction and retire it after the bubble's lifetime.
  const showReaction = (next: { intent?: string; bubble?: string }) => {
    setReaction(next);
    if (reactionTimer.current !== undefined) {
      window.clearTimeout(reactionTimer.current);
    }
    reactionTimer.current = window.setTimeout(() => {
      reactionTimer.current = undefined;
      setReaction({});
    }, 2200);
  };

  useEffect(() => {
    return Events.On('pet:state', (event) => {
      const next = toPetView(event.data as PetStatePayload | null);
      const previous = petRef.current;
      petRef.current = next;
      setPet(next);
      riveRef.current?.apply(next);
      if (!hiddenRef.current) {
        // Any fresh payload wakes the renderer back up.
        riveRef.current?.play();
        scheduleIdlePause();
      }
      // One-shots replay only when the intent sequence moved; the
      // director re-broadcasts the reaction it is already playing.
      const freshIntent = next.intentSeq !== lastIntentSeqRef.current;
      if (freshIntent) lastIntentSeqRef.current = next.intentSeq;
      if (next.sleeping && !previous.sleeping) {
        // Falling asleep is a value change, so it has no intent sequence
        // of its own; greet it once with the nap bubble.
        showReaction({ intent: 'nap' });
        return;
      }
      if (freshIntent && (next.intent || next.bubble)) {
        showReaction({ intent: next.intent, bubble: next.bubble });
      }
    });
  }, []);

  useEffect(
    () => () => {
      if (reactionTimer.current !== undefined) {
        window.clearTimeout(reactionTimer.current);
      }
      if (pauseTimer.current !== undefined) {
        window.clearTimeout(pauseTimer.current);
      }
    },
    [],
  );

  // Hidden windows cost nothing: stop the rAF loop while the pet is
  // not on screen and pick up where it left off when it returns.
  useEffect(() => {
    const onVisibility = () => {
      hiddenRef.current = document.hidden;
      if (document.hidden) {
        if (pauseTimer.current !== undefined) {
          window.clearTimeout(pauseTimer.current);
          pauseTimer.current = undefined;
        }
        riveRef.current?.pause();
        return;
      }
      riveRef.current?.play();
      scheduleIdlePause();
    };
    document.addEventListener('visibilitychange', onVisibility);
    return () => document.removeEventListener('visibilitychange', onVisibility);
  }, []);

  // Reload the pack when plugins register/unregister packs or the user
  // changes the assistant character in settings.
  useEffect(() => {
    return Events.On('opencraft:ui', (event) => {
      const ev = event.data as { type?: string } | null;
      if (
        ev?.type === 'pet:packs_changed' ||
        ev?.type === 'pet:settings_changed'
      ) {
        setReloadToken((n) => n + 1);
      }
    });
  }, []);

  // Select the active pack and fetch its .riv bytes.
  useEffect(() => {
    let alive = true;
    let selected: PetPack | null = null;
    setPending(null);
    setRiveReady(false);
    setStatus(null);
    setMountError(null);
    void (async () => {
      try {
        const [prefs, packs] = await Promise.all([
          api.petSettings(),
          api.petListPacks(),
        ]);
        const pack = pickPack(prefs.assistantCharacter, packs);
        if (!alive || !pack) return;
        selected = pack;
        // Name the character before its bytes land, so the surface and
        // the diagnostics panel can say which one failed to mount.
        setActivePack(pack);
        if (!pack.rivAsset) {
          // A pack that names no asset can never render; say so instead
          // of leaving an empty window behind.
          const failed = unmountedStatus(
            pack,
            `pet: pack ${pack.id} has no riv asset`,
          );
          setStatus(failed);
          reportRuntimeStatus(failed);
          return;
        }
        const base64 = await api.petPackAsset(pack.rivAsset);
        if (!alive) return;
        setPending({
          pack,
          bytes: base64ToArrayBuffer(base64),
        });
      } catch (err) {
        console.error('pet: pack load failed', err);
        if (!alive) return;
        setMountError(String(err));
        if (selected) {
          const failed = unmountedStatus(selected, String(err));
          setStatus(failed);
          reportRuntimeStatus(failed);
        }
      }
    })();
    return () => {
      alive = false;
    };
  }, [reloadToken]);

  // Mount the Rive runtime once bytes arrive.
  useEffect(() => {
    if (!pending) return;
    const canvas = canvasRef.current;
    if (!canvas) return;
    let cancelled = false;
    let handle: PetRiveHandle | null = null;
    void createPetRive(canvas, pending.bytes, pending.pack)
      .then((created) => {
        if (cancelled) {
          created.destroy();
          return;
        }
        handle = created;
        riveRef.current = created;
        setRiveReady(true);
        created.resize();
        created.apply(petRef.current);
        const mounted = created.status();
        setStatus(mounted);
        // The renderer is the only side that has the asset loaded, so
        // it reports the match result back for the diagnostics panel.
        reportRuntimeStatus(mounted);
        scheduleIdlePause();
      })
      .catch((err) => {
        console.error('pet: rive mount failed', err);
        if (cancelled) return;
        setMountError(String(err));
        const failed = unmountedStatus(pending.pack, String(err));
        setStatus(failed);
        reportRuntimeStatus(failed);
      });
    return () => {
      cancelled = true;
      riveRef.current = null;
      handle?.destroy();
    };
  }, [pending]);

  // Keep the Rive drawing surface sized with the window.
  useEffect(() => {
    const onResize = () => riveRef.current?.resize();
    window.addEventListener('resize', onResize);
    return () => window.removeEventListener('resize', onResize);
  }, []);

  const onPointerDown = (event: React.PointerEvent) => {
    const drag = dragRef.current;
    drag.active = true;
    drag.startScreenX = event.screenX;
    drag.startScreenY = event.screenY;
    drag.moved = 0;
    drag.startWin = null;
    drag.desired = null;
    event.currentTarget.setPointerCapture(event.pointerId);
    // The window chases the cursor during a drag, so window-relative
    // coordinates cancel out. Anchor on the window position captured
    // here and use absolute screen deltas from then on.
    void api.petGetPosition().then((pos) => {
      if (!drag.active || !pos.ready) return;
      drag.startWin = { x: pos.x, y: pos.y };
      requestDragApply();
    });
  };

  const bubbleForIntent = (intent?: string) => {
    switch (intent) {
      case 'welcome':
        return t('config.petWelcomeBack');
      case 'wave':
        return t('config.petWave');
      case 'sulk':
        return t('config.petSulk');
      case 'zoomies':
        return t('config.petZoomies');
      case 'nap':
        return t('config.petNap');
      default:
        return undefined;
    }
  };
  const reactionBubble = reaction.bubble ?? bubbleForIntent(reaction.intent);
  const bubbleText = pet.disposition === 'ask' ? '…?' : reactionBubble;
  // A pack that does not match its asset renders nothing useful; say so
  // on the surface (and in Settings > Diagnostics) instead of leaving a
  // frozen sprite behind an invisible window.
  const degraded = Boolean(mountError) || (status !== null && !status.ok);
  const degradedDetail =
    mountError || status?.error || status?.missing?.join('\n') || '';

  const onPointerMove = (event: React.PointerEvent) => {
    const drag = dragRef.current;
    if (!drag.active) return;
    drag.moved +=
      Math.abs(event.screenX - drag.startScreenX) +
      Math.abs(event.screenY - drag.startScreenY);
    if (!drag.startWin) return;
    drag.desired = {
      x: drag.startWin.x + Math.round(event.screenX - drag.startScreenX),
      y: drag.startWin.y + Math.round(event.screenY - drag.startScreenY),
    };
    requestDragApply();
  };

  const requestDragApply = () => {
    const drag = dragRef.current;
    if (drag.raf) return;
    drag.raf = window.requestAnimationFrame(() => {
      drag.raf = 0;
      if (!drag.active || !drag.startWin || !drag.desired || drag.inFlight) {
        return;
      }
      const target = drag.desired;
      drag.inFlight = true;
      void api.petSetPosition(target.x, target.y).finally(() => {
        drag.inFlight = false;
        if (
          drag.active &&
          drag.desired &&
          (drag.desired.x !== target.x || drag.desired.y !== target.y)
        ) {
          requestDragApply();
        }
      });
    });
  };

  const cancelDrag = () => {
    const drag = dragRef.current;
    drag.active = false;
    drag.desired = null;
    if (drag.raf) {
      window.cancelAnimationFrame(drag.raf);
      drag.raf = 0;
    }
  };

  const endDrag = (event: React.PointerEvent) => {
    const drag = dragRef.current;
    if (!drag.active) return;
    const total =
      Math.abs(event.screenX - drag.startScreenX) +
      Math.abs(event.screenY - drag.startScreenY);
    if (total < 6) {
      cancelDrag();
      void api.petPoke();
      void api.petActivate();
      return;
    }
    cancelDrag();
  };

  const onPointerLeave = () => {
    cancelDrag();
  };

  return (
    <main
      className="pet-surface"
      data-interactive={pet.interactive ? 'true' : 'false'}
      data-rive-ready={riveReady ? 'true' : 'false'}
      data-runtime-status={degraded ? 'degraded' : 'ok'}
      data-pack={activePack?.id ?? ''}
      data-intent={reaction.intent ?? ''}
      data-phase={pet.phase}
      style={
        { '--pet-scale': activePack?.meta.scale ?? 1 } as React.CSSProperties
      }
      onPointerDown={onPointerDown}
      onPointerMove={onPointerMove}
      onPointerUp={endDrag}
      onPointerCancel={endDrag}
      onPointerLeave={onPointerLeave}
    >
      <canvas ref={canvasRef} className="pet-canvas" />
      {degraded && (
        <div
          className="pet-degraded"
          data-testid="pet-degraded"
          title={degradedDetail}
        >
          !
        </div>
      )}
      {bubbleText && <div className="pet-bubble">{bubbleText}</div>}
      {pet.toolName && (
        <div className="pet-tool-label" title={pet.toolName}>
          {pet.toolName}
        </div>
      )}
    </main>
  );
}
