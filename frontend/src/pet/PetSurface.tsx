import { useEffect, useRef, useState } from 'react';
import type * as React from 'react';
import { Events } from '@wailsio/runtime';
import { useTranslation } from 'react-i18next';
import { api } from '../lib/api';
import type { PetPack } from './pack';
import { pickPack } from './pack';
import {
  base64ToArrayBuffer,
  createPetRive,
  type PetRiveHandle,
} from './rive';
import {
  toPetView,
  type PetStatePayload,
  type PetView,
} from './state';
import './pet.css';

interface PendingRivePack {
  pack: PetPack;
  bytes: ArrayBuffer;
}

/**
 * PetSurface is the whole-screen roaming pet renderer mounted by the
 * pet Wails window (?surface=pet). It is deliberately inert: no plugin
 * host, no main store. The Go PetDirector broadcasts pet:state
 * snapshots; the surface resolves the active pack, mounts the Rive
 * state machine on a canvas, and drives it with those snapshots.
 */
export default function PetSurface() {
  const { t } = useTranslation();
  const [pet, setPet] = useState<PetView>(() => toPetView(null));
  const [riveReady, setRiveReady] = useState(false);
  const [pending, setPending] = useState<PendingRivePack | null>(null);
  const [reloadToken, setReloadToken] = useState(0);
  const [reaction, setReaction] = useState<{
    intent?: string;
    bubble?: string;
  }>({});
  const [menu, setMenu] = useState<{ x: number; y: number } | null>(null);
  const [roamPaused, setRoamPaused] = useState(false);
  const reactionTimer = useRef<number | undefined>(undefined);
  const canvasRef = useRef<HTMLCanvasElement | null>(null);
  const riveRef = useRef<PetRiveHandle | null>(null);
  const petRef = useRef(pet);
  petRef.current = pet;
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

  useEffect(() => {
    return Events.On('pet:state', (event) => {
      const next = toPetView(event.data as PetStatePayload | null);
      setPet(next);
      riveRef.current?.apply(next);
      if (next.intent || next.bubble) {
        setReaction({ intent: next.intent, bubble: next.bubble });
        if (reactionTimer.current !== undefined) {
          window.clearTimeout(reactionTimer.current);
        }
        reactionTimer.current = window.setTimeout(() => {
          reactionTimer.current = undefined;
          setReaction({});
        }, 2200);
      }
    });
  }, []);

  useEffect(
    () => () => {
      if (reactionTimer.current !== undefined) {
        window.clearTimeout(reactionTimer.current);
      }
    },
    [],
  );

  // Reload the pack when plugins register/unregister packs or the user
  // changes the assistant character in settings.
  useEffect(() => {
    return Events.On('opencraft:ui', (event) => {
      const ev = event.data as { type?: string } | null;
      if (ev?.type === 'pet:packs_changed' ||
          ev?.type === 'pet:settings_changed') {
        setReloadToken((n) => n + 1);
      }
    });
  }, []);

  // Select the active pack and fetch its .riv bytes.
  useEffect(() => {
    let alive = true;
    setPending(null);
    setRiveReady(false);
    void (async () => {
      try {
        const [prefs, packs] = await Promise.all([
          api.petSettings(),
          api.petListPacks(),
        ]);
        const pack = pickPack(prefs.assistantCharacter, packs);
        if (!alive || !pack?.rivAsset) return;
        const base64 = await api.petPackAsset(pack.rivAsset);
        if (!alive) return;
        setPending({
          pack,
          bytes: base64ToArrayBuffer(base64),
        });
      } catch (err) {
        console.warn('pet: pack load failed, staying transparent', err);
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
      })
      .catch((err) => {
        console.warn('pet: rive mount failed', err);
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
  const bubbleText =
    pet.disposition === 'ask' ? '…?' : reactionBubble;

  const onPointerMove = (event: React.PointerEvent) => {
    const drag = dragRef.current;
    if (!drag.active) return;
    drag.moved += Math.abs(event.screenX - drag.startScreenX) +
      Math.abs(event.screenY - drag.startScreenY);
    if (!drag.startWin) return;
    drag.desired = {
      x: drag.startWin.x +
        Math.round(event.screenX - drag.startScreenX),
      y: drag.startWin.y +
        Math.round(event.screenY - drag.startScreenY),
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
    const total = Math.abs(event.screenX - drag.startScreenX) +
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

  const onContextMenu = (event: React.MouseEvent) => {
    event.preventDefault();
    setMenu({ x: event.clientX, y: event.clientY });
  };

  const closeMenu = () => setMenu(null);

  const toggleRoamPaused = () => {
    const next = !roamPaused;
    setRoamPaused(next);
    closeMenu();
    void api.petSetRoamingPaused(next);
  };

  const disablePet = () => {
    closeMenu();
    void api.setPetSettings({ enabled: false });
  };

  return (
    <main
      className="pet-surface"
      data-interactive={pet.interactive ? 'true' : 'false'}
      data-rive-ready={riveReady ? 'true' : 'false'}
      data-intent={reaction.intent ?? ''}
      data-phase={pet.phase}
      onPointerDown={onPointerDown}
      onPointerMove={onPointerMove}
      onPointerUp={endDrag}
      onPointerCancel={endDrag}
      onPointerLeave={onPointerLeave}
      onContextMenu={onContextMenu}
    >
      <canvas ref={canvasRef} className="pet-canvas" />
      {bubbleText && <div className="pet-bubble">{bubbleText}</div>}
      {pet.toolName && (
        <div className="pet-tool-label" title={pet.toolName}>
          {pet.toolName}
        </div>
      )}
      {menu && (
        <>
          <div className="pet-menu-overlay" onClick={closeMenu} />
          <div
            className="pet-menu"
            style={{ left: menu.x, top: menu.y }}
            role="menu"
          >
            <button role="menuitem" onClick={toggleRoamPaused}>
              {roamPaused
                ? t('config.petMenuResume')
                : t('config.petMenuPause')}
            </button>
            <button role="menuitem" onClick={() => { closeMenu(); void api.petActivate(); }}>
              {t('config.petMenuOpenMain')}
            </button>
            <button
              role="menuitem"
              className="pet-menu-danger"
              onClick={disablePet}
            >
              {t('config.petMenuDisable')}
            </button>
          </div>
        </>
      )}
    </main>
  );
}
