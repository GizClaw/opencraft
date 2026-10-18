import { PetStateChannel, UIEventChannel, UIEventType } from '../lib/events';
import { useEffect, useRef, useState } from 'react';
import type * as React from 'react';
import { Events } from '@wailsio/runtime';
import { CircleAlert } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { api } from '../lib/api';
import { measurePetGeometry } from './geometry';
import { hitTestPet } from './hit';
import type { PetPack } from './pack';
import { pickPack } from './pack';
import { base64ToArrayBuffer, createPetRive, type PetRiveHandle } from './rive';
import { toPetView, type PetStatePayload, type PetView } from './state';
import { petToolCategory, petToolIcon } from './toolCategory';
import type { PetRuntimeStatus } from './validate';
import './pet.css';
import { ICON } from '../components/ui/icon';

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

// petGeometryHeartbeat is the slow re-measure that covers what no
// snapshot announces: an animation running inside one state (the walk
// cycle, idle breathing) keeps drawing without a new payload. Go parks
// the window and computes the watch spot from the box, so the box has
// to stay honest, but the measurement is a GPU→CPU readback of the
// whole canvas — the one thing on this surface that must not run on a
// fast clock. Pose changes measure on their own (see the sampler
// effect), and a frozen frame cannot measure differently from the last
// read, so a parked or hidden surface skips the heartbeat outright.
const petGeometryHeartbeat = 2_000;

// petGeometrySettleFrames is how long a pose change waits before its
// pixels are read. A snapshot writes the view model; Rive draws it on
// its own animation frame, so a read in the same task (or the next one,
// whichever loop runs first) still sees the pose before it.
const petGeometrySettleFrames = 2;

/**
 * PetSurface is the whole-screen pet renderer mounted by the
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
  // pausedRef tracks whether this surface paused the renderer. Rive's
  // play() tears its pointer listeners down and re-creates them and
  // restarts the render loop, so resuming a runtime that was never
  // paused costs the whole listener setup for nothing — and the
  // director broadcasts a snapshot on every phase, tool and intent
  // change.
  const pausedRef = useRef(false);
  const surfaceRef = useRef<HTMLElement | null>(null);
  const lastIntentSeqRef = useRef(0);
  const reportedGeometryRef = useRef('');
  const canvasRef = useRef<HTMLCanvasElement | null>(null);
  const riveRef = useRef<PetRiveHandle | null>(null);
  // requestMeasureRef is how the paths that move the character ask the
  // sampler below for a read: the surface state handler (a pose change)
  // and the resize listener (a new drawing surface). It is written by
  // the sampler effect, which owns the pending frame.
  const requestMeasureRef = useRef<(frames?: number) => void>(() => {});
  const measureFrameRef = useRef(0);
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

  const pauseRive = () => {
    if (pausedRef.current) return;
    pausedRef.current = true;
    riveRef.current?.pause();
  };

  // resumeRive is the only caller of play(): playback is started either
  // by a fresh runtime (the mount effect) or by waking a paused one.
  const resumeRive = () => {
    if (!pausedRef.current) return;
    pausedRef.current = false;
    riveRef.current?.play();
  };

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
        // Still walking: the walk cycle has to keep playing.
        scheduleIdlePause();
        return;
      }
      if (!idle) return;
      pauseRive();
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
    return Events.On(PetStateChannel, (event) => {
      const next = toPetView(event.data as PetStatePayload | null);
      const previous = petRef.current;
      petRef.current = next;
      setPet(next);
      riveRef.current?.apply(next);
      // A new pose is a new picture: read it back once it has been
      // drawn instead of waiting for the heartbeat.
      requestMeasureRef.current(petGeometrySettleFrames);
      if (!hiddenRef.current) {
        // Any fresh payload wakes the renderer back up — but only one
        // that is actually parked: play() on a running runtime rebuilds
        // its listeners and restarts the loop for no reason.
        resumeRive();
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
        pauseRive();
        return;
      }
      resumeRive();
      scheduleIdlePause();
      // The window may have been resized (or the character moved) while
      // nothing was drawing; the heartbeat did not run for it either.
      requestMeasureRef.current(petGeometrySettleFrames);
    };
    document.addEventListener('visibilitychange', onVisibility);
    return () => document.removeEventListener('visibilitychange', onVisibility);
  }, []);

  // Reload the pack when plugins register/unregister packs or the user
  // changes the assistant character in settings.
  useEffect(() => {
    return Events.On(UIEventChannel, (event) => {
      const ev = event.data as { type?: string } | null;
      if (
        ev?.type === UIEventType.petPacksChanged ||
        ev?.type === UIEventType.petSettingsChanged
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
        // A freshly created runtime is playing; only a pause() this
        // surface performed may be undone by a later play().
        pausedRef.current = false;
        setRiveReady(true);
        created.resize();
        created.apply(petRef.current);
        // The first frame this runtime draws is the first one worth
        // measuring: until now the canvas held nothing to read.
        requestMeasureRef.current(petGeometrySettleFrames);
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
    const onResize = () => {
      riveRef.current?.resize();
      // The art box is measured in CSS pixels against the surface, so a
      // resized window moves every number in the report.
      requestMeasureRef.current(petGeometrySettleFrames);
    };
    window.addEventListener('resize', onResize);
    return () => window.removeEventListener('resize', onResize);
  }, []);

  // Tell Go where the character is actually drawn. The OS window is a
  // transparent stage around it, so the dock position and the watch
  // spot would otherwise be computed against the wrong rectangle.
  //
  // The sampler runs on two signals and one slow fallback. A pose
  // change and a resize each ask for a read once their frame has been
  // drawn (they are the moments the box can move for a reason we know);
  // the heartbeat covers the animation playing inside a single state.
  // Nothing here measures on a fast clock: the readback is the
  // expensive part, and its cost has to follow the picture.
  useEffect(() => {
    const measure = () => {
      const geometry = measurePetGeometry(
        canvasRef.current,
        surfaceRef.current,
      );
      if (!geometry) return;
      const key = JSON.stringify(geometry);
      if (key === reportedGeometryRef.current) return;
      reportedGeometryRef.current = key;
      void api.petReportGeometry(geometry).catch((err) => {
        console.warn('pet: geometry report failed', err);
      });
    };
    // requestMeasure waits out `frames` animation frames before
    // reading, and a second request replaces the one already waiting:
    // only the newest picture is worth measuring.
    const requestMeasure = (frames = 0) => {
      if (measureFrameRef.current !== 0) {
        cancelAnimationFrame(measureFrameRef.current);
      }
      let left = frames;
      const tick = () => {
        if (left > 0) {
          left -= 1;
          measureFrameRef.current = requestAnimationFrame(tick);
          return;
        }
        measureFrameRef.current = 0;
        measure();
      };
      measureFrameRef.current = requestAnimationFrame(tick);
    };
    requestMeasureRef.current = requestMeasure;
    requestMeasure();
    // A parked runtime and a hidden window both keep the last frame on
    // screen, so re-reading the pixels cannot change the answer — the
    // heartbeat is skipped, not deferred into a burst on wake.
    const timer = window.setInterval(() => {
      if (pausedRef.current || document.hidden) return;
      requestMeasure();
    }, petGeometryHeartbeat);
    return () => {
      window.clearInterval(timer);
      if (measureFrameRef.current !== 0) {
        cancelAnimationFrame(measureFrameRef.current);
        measureFrameRef.current = 0;
      }
      requestMeasureRef.current = () => {};
    };
  }, []);

  const onPointerDown = (event: React.PointerEvent) => {
    // The stage is a transparent rectangle the OS window keeps
    // capturing, so only a press on something the user can see starts
    // an interaction: the drawn character, or one of the overlays that
    // belongs to it (the speech bubble, the tool pill, and the degraded
    // badge, which is the only thing on screen when the pack does not
    // match its asset). Presses on the transparent part of the stage
    // belong to whatever is underneath — see pet/hit.ts for the pixel
    // probe.
    const target = event.target as Element | null;
    const onOverlay = Boolean(
      target?.closest?.('.pet-bubble, .pet-tool, .pet-degraded'),
    );
    if (
      !onOverlay &&
      !hitTestPet(canvasRef.current, event.clientX, event.clientY)
    ) {
      return;
    }
    const drag = dragRef.current;
    drag.active = true;
    // A press is the last moment the box is worth reading: the drag and
    // the dock spot that follows it are anchored on what is on screen
    // now, not on what the heartbeat last saw.
    requestMeasureRef.current();
    // The pet walks while the window is dragged; Go turns the gesture
    // into walking/facing values (see beginPetDrag).
    void api.petBeginDrag().catch((err) => {
      console.warn('pet: begin drag failed', err);
    });
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
  // A tool that failed keeps its name on the stage but flags itself, so
  // the desktop still says which call went wrong.
  const toolFailed = pet.phase === 'error';
  const ToolIcon = toolFailed ? CircleAlert : petToolIcon(pet.toolCategory);

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

  // finishDrag closes the gesture for Go, which is what stops the walk
  // cycle when the user lets go.
  const finishDrag = () => {
    if (!dragRef.current.active) return;
    cancelDrag();
    void api.petEndDrag().catch((err) => {
      console.warn('pet: end drag failed', err);
    });
  };

  const endDrag = (event: React.PointerEvent) => {
    const drag = dragRef.current;
    if (!drag.active) return;
    const total =
      Math.abs(event.screenX - drag.startScreenX) +
      Math.abs(event.screenY - drag.startScreenY);
    if (total < 6) {
      finishDrag();
      void api.petPoke();
      void api.petActivate();
      return;
    }
    finishDrag();
  };

  const onPointerLeave = () => {
    finishDrag();
  };

  return (
    <main
      ref={surfaceRef}
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
          data-tip={degradedDetail}
        >
          !
        </div>
      )}
      {bubbleText && <div className="pet-bubble">{bubbleText}</div>}
      {pet.toolName && (
        <div
          className="pet-tool"
          data-category={petToolCategory(pet.toolCategory)}
          data-state={toolFailed ? 'error' : 'running'}
          data-tip={pet.toolName}
        >
          <span className="pet-tool__badge" aria-hidden="true">
            <ToolIcon size={ICON.xs} strokeWidth={2.4} />
          </span>
          <span className="pet-tool__name">{pet.toolName}</span>
        </div>
      )}
    </main>
  );
}
