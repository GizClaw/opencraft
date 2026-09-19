import { useCallback, useLayoutEffect, useState, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { ArrowLeft, File as FileGlyph } from 'lucide-react';
import { followLinkTarget } from '../../lib/linkTarget';
import { useStore } from '../../lib/store';
import type { FileTab, ResolvedTarget } from '../../lib/types';
import type { MarkdownLinkHandler } from '../Markdown';
import { Modal } from '../ui/Modal';
import { ICON } from '../ui/icon';
import { FilePreviewPane } from './FilePreviewPane';

// FilePreviewModal shows one document link's target as a centered
// dialog. Surfaces without a file rail of their own (the skills page)
// route their markdown references here instead of the chat's viewer
// panel, which belongs to the session; nested references push another
// page on the same dialog, so a SKILL.md reference chain never leaves
// it (the back arrow pops one page).
export function FilePreviewModal({
  initial,
  onClose,
}: {
  initial: ResolvedTarget;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const flash = useStore((s) => s.flash);
  const [pages, setPages] = useState<ResolvedTarget[]>([initial]);
  const active = pages[pages.length - 1];

  // A layout effect, not a passive one: Escape must belong to this
  // dialog from the commit that put it on screen. A passive effect
  // leaves a window between the dialog appearing and the listener being
  // installed, and an Escape landing there is swallowed by whatever
  // surface owns the key underneath (the page this was opened from),
  // leaving the dialog open.
  useLayoutEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return;
      // This dialog can open above another overlay (the PR detail page
      // and the skill drawer both push it), so Escape belongs to the
      // top layer: consuming it here keeps the page underneath open.
      event.stopPropagation();
      onClose();
    };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [onClose]);

  // A directory has no page of its own, so it goes to the system file
  // manager; files push one more page on this dialog.
  const follow = (href: string, base: string) =>
    void followLinkTarget(href, base, {
      openFile: (target) => setPages((prev) => [...prev, target]),
      onError: flash,
    });

  const path = active.rel || active.path;
  return (
    <Modal
      open
      onClose={onClose}
      title={active.name}
      icon={FileGlyph}
      width="min(66rem, 94vw)"
      panelClassName="h-[84vh]"
      bodyClassName="overflow-hidden p-0"
    >
      <div className="flex h-full min-h-0 flex-col">
        <div className="flex h-8 shrink-0 items-center gap-1 border-b border-edge px-2 text-xs text-dim">
          {pages.length > 1 && (
            <button
              onClick={() => setPages((prev) => prev.slice(0, -1))}
              className="rounded-tight p-0.5 hover:bg-panel2 hover:text-fg"
              title={t('files.linkBack')}
              aria-label={t('files.linkBack')}
            >
              <ArrowLeft size={ICON.sm} />
            </button>
          )}
          <span className="min-w-0 flex-1 truncate font-mono" title={path}>
            {path}
          </span>
        </div>
        <div className="min-h-0 flex-1">
          <FilePreviewPane
            key={active.path}
            tab={tabOf(active)}
            onOpen={(href, base) => void follow(href, base)}
          />
        </div>
      </div>
    </Modal>
  );
}

// tabOf adapts a resolved target to the viewer tab shape the preview
// pane renders from.
function tabOf(target: ResolvedTarget): FileTab {
  return {
    key: target.path,
    path: target.path,
    rel: target.rel,
    root: target.root,
    name: target.name,
    media_type: target.media_type ?? '',
  };
}

// useFilePreview wires one overlay's document links to a preview
// dialog: openLink is a drop-in Markdown handler and the returned modal
// node renders the files it resolves. Surfaces without a file rail of
// their own (the skills page, the PR detail page) use this instead of
// the chat's viewer panel, which belongs to the session.
export function useFilePreview(): {
  openLink: MarkdownLinkHandler;
  modal: ReactNode;
} {
  const flash = useStore((s) => s.flash);
  const [target, setTarget] = useState<ResolvedTarget | null>(null);
  // Stable identities: every parent render would otherwise hand the
  // dialog a new onClose, tearing the Escape listener down and back up
  // for no reason (and reopening the gap it is meant to close).
  const close = useCallback(() => setTarget(null), []);
  const openLink = useCallback<MarkdownLinkHandler>(
    (href, base = '') =>
      void followLinkTarget(href, base, {
        openFile: setTarget,
        onError: flash,
      }),
    [flash],
  );
  return {
    openLink,
    modal: target ? (
      <FilePreviewModal initial={target} onClose={close} />
    ) : null,
  };
}
