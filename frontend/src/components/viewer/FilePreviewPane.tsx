import { Component, lazy, Suspense, useEffect, useState } from 'react';
import type { ErrorInfo, ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { File as FileGlyph, Loader2 } from 'lucide-react';
import i18n from '../../i18n';
import { api } from '../../lib/api';
import { reportFrontendError } from '../../lib/frontendErrors';
import { useStore } from '../../lib/store';
import type { FilePreview, FileTab } from '../../lib/types';
import { Markdown } from '../Markdown';
import { ICON } from '../ui/icon';

const CodePane = lazy(() =>
  import('./CodePane').then((m) => ({ default: m.CodePane })),
);
const PdfPane = lazy(() =>
  import('./PdfPane').then((m) => ({ default: m.PdfPane })),
);

// LazyBoundary contains one lazy preview chunk. A chunk that fails to
// load (e.g. a stale Vite optimizer serving an old module list) shows
// a retry card inside the viewer instead of crashing the whole app.
class LazyBoundary extends Component<
  { children: ReactNode },
  { error: Error | null }
> {
  state: { error: Error | null } = { error: null };

  static getDerivedStateFromError(error: Error) {
    return { error };
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    reportFrontendError('file-viewer-chunk', error, info.componentStack ?? '');
  }

  render() {
    if (this.state.error) {
      return (
        <div className="flex h-full flex-col items-center justify-center gap-3 p-6 text-center text-xs text-dim">
          <p className="text-err">{i18n.t('files.viewerChunkFailed')}</p>
          <code className="max-w-full truncate rounded-tight border border-edge bg-panel2 px-2 py-1">
            {String(this.state.error.message)}
          </code>
          <button
            onClick={() => this.setState({ error: null })}
            className="rounded-control border border-edge bg-panel2 px-3 py-1.5 text-fg hover:border-accent/50"
          >
            {i18n.t('app.tryAgain')}
          </button>
        </div>
      );
    }
    return this.props.children;
  }
}

// isMarkdownFile reports whether a file renders as formatted markdown
// instead of source. Shared with the viewer's tab chips.
export function isMarkdownFile(name: string): boolean {
  const ext = name.split('.').pop()?.toLowerCase();
  return ext === 'md' || ext === 'markdown';
}

// dirOfPath is the slash-separated parent of a relative path, used as
// the base markdown links resolve against.
export function dirOfPath(rel: string): string {
  const i = Math.max(rel.lastIndexOf('/'), rel.lastIndexOf('\\'));
  return i < 0 ? '.' : rel.slice(0, i);
}

// FilePreviewPane renders one file's preview payload: markdown, source
// code, image, video, PDF, or the meta fallback. It is the shared body
// of two hosts — the chat rail's viewer tabs and the preview dialog —
// so it fills its parent box and scrolls internally.
export function FilePreviewPane({
  tab,
  onOpen,
}: {
  tab: FileTab;
  // onOpen routes document links inside a rendered markdown file. The
  // chat rail opens them as another viewer tab; the dialog opens them
  // as another page of the same dialog.
  onOpen: (href: string, base: string) => void;
}) {
  const { t } = useTranslation();
  const [preview, setPreview] = useState<FilePreview | null>(null);
  const [error, setError] = useState('');
  const [showMd, setShowMd] = useState(isMarkdownFile(tab.name));
  const [videoFailed, setVideoFailed] = useState(false);

  useEffect(() => {
    let live = true;
    setPreview(null);
    setError('');
    setVideoFailed(false);
    void api
      .readPreview(tab.path)
      .then((p) => {
        if (live) setPreview(p);
      })
      .catch((err) => {
        if (live) setError(String(err));
      });
    return () => {
      live = false;
    };
  }, [tab.path]);

  if (error) {
    return <FileMetaPane tab={tab} message={error} showMetaActions />;
  }
  if (!preview) {
    return (
      <div className="flex h-full items-center justify-center text-dim">
        <Loader2 size={ICON.md} className="animate-spin" />
      </div>
    );
  }

  if (preview.kind === 'text' && isMarkdownFile(tab.name) && showMd) {
    // Relative references resolve against the file's own directory.
    // Workspace tabs have a relative path; data/skill tabs fall back to
    // the absolute path so dirOfPath stays correct for both.
    const base =
      tab.root === 'workspace' ? dirOfPath(tab.rel) : dirOfPath(tab.path);
    return (
      <div className="h-full overflow-y-auto p-4">
        <button
          onClick={() => setShowMd(false)}
          className="mb-2 rounded-tight border border-edge bg-panel2 px-2 py-1 text-xs text-dim hover:text-fg"
        >
          {t('files.viewSource')}
        </button>
        <div className="prose-chat text-sm">
          <Markdown
            text={preview.text ?? ''}
            basePath={base}
            onOpen={(href, b) => onOpen(href, b ?? '')}
          />
        </div>
      </div>
    );
  }

  if (preview.kind === 'text') {
    return (
      <LazyBoundary>
        <Suspense
          fallback={
            <div className="flex h-full items-center justify-center text-dim">
              <Loader2 size={ICON.md} className="animate-spin" />
            </div>
          }
        >
          <CodePane
            text={preview.text ?? ''}
            name={tab.name}
            sourceView={isMarkdownFile(tab.name)}
            onSource={() => setShowMd(true)}
          />
        </Suspense>
      </LazyBoundary>
    );
  }

  if (preview.kind === 'image' && preview.data_url) {
    return (
      <div className="flex h-full items-start justify-center overflow-auto p-4">
        <img
          src={preview.data_url}
          alt={tab.name}
          className="max-w-full rounded-control border border-edge object-contain"
        />
      </div>
    );
  }

  if (preview.kind === 'video' && preview.stream_url && !videoFailed) {
    // The loopback endpoint serves byte ranges, so the player streams
    // and seeks without pulling the file through the IPC bridge.
    return (
      <div className="flex h-full items-center justify-center overflow-auto bg-black/95 p-4">
        <video
          src={preview.stream_url}
          controls
          preload="metadata"
          onError={() => setVideoFailed(true)}
          className="max-h-full max-w-full rounded-control border border-edge"
        />
      </div>
    );
  }

  if (preview.kind === 'video') {
    // A platform that refuses the loopback URL keeps the system-player
    // fallback instead of showing a broken player.
    return (
      <FileMetaPane
        tab={tab}
        message={t('files.videoUnavailable')}
        showMetaActions
      />
    );
  }

  if (preview.kind === 'pdf' && preview.data_url) {
    return (
      <LazyBoundary>
        <Suspense
          fallback={
            <div className="flex h-full items-center justify-center text-dim">
              <Loader2 size={ICON.md} className="animate-spin" />
            </div>
          }
        >
          <PdfPane dataUrl={preview.data_url} name={tab.name} />
        </Suspense>
      </LazyBoundary>
    );
  }

  return (
    <FileMetaPane
      tab={tab}
      message={preview.too_large ? t('files.tooLarge') : undefined}
      showMetaActions
    />
  );
}

// FileMetaPane is the fallback for files with no inline preview: the
// name, the reason, and the two system hand-offs.
export function FileMetaPane({
  tab,
  message,
  showMetaActions = true,
}: {
  tab: FileTab;
  message?: string;
  showMetaActions?: boolean;
}) {
  const { t } = useTranslation();
  const flash = useStore((s) => s.flash);
  return (
    <div className="flex h-full flex-col items-center justify-center gap-3 p-6 text-center text-xs text-dim">
      <FileGlyph size={ICON.hero} className="opacity-50" />
      <div className="break-all text-fg">{tab.name}</div>
      <div>
        {message ?? `${t('files.noInlinePreview')} ${tab.media_type || ''}`}
      </div>
      {showMetaActions && (
        <div className="flex gap-2">
          <button
            onClick={() =>
              void api.openPath(tab.path).catch((err) => flash(String(err)))
            }
            className="rounded-control border border-edge bg-panel2 px-3 py-1.5 hover:border-accent/50"
          >
            {t('files.openSystem')}
          </button>
          <button
            onClick={() =>
              void api
                .revealArtifact(tab.path)
                .catch((err) => flash(String(err)))
            }
            className="rounded-control border border-edge bg-panel2 px-3 py-1.5 hover:border-accent/50"
          >
            {t('files.reveal')}
          </button>
        </div>
      )}
    </div>
  );
}
