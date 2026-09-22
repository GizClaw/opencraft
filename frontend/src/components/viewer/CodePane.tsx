import { useEffect, useMemo, useRef } from 'react';
import { useTranslation } from 'react-i18next';
import CodeMirror from '@uiw/react-codemirror';
import { EditorView } from '@codemirror/view';
import { StreamLanguage } from '@codemirror/language';
import { javascript } from '@codemirror/lang-javascript';
import { python } from '@codemirror/lang-python';
import { java } from '@codemirror/lang-java';
import { cpp } from '@codemirror/lang-cpp';
import { html } from '@codemirror/lang-html';
import { css } from '@codemirror/lang-css';
import { json } from '@codemirror/lang-json';
import { sql } from '@codemirror/lang-sql';
import { markdown } from '@codemirror/lang-markdown';
import { xml } from '@codemirror/lang-xml';
import { go } from '@codemirror/legacy-modes/mode/go';
import { rust } from '@codemirror/legacy-modes/mode/rust';
import { shell } from '@codemirror/legacy-modes/mode/shell';
import { yaml } from '@codemirror/legacy-modes/mode/yaml';
import { toml } from '@codemirror/legacy-modes/mode/toml';
import type { Extension } from '@codemirror/state';
import { Eye } from 'lucide-react';
import { ICON } from '../ui/icon';
import {
  classifyMarks,
  layoutSignature,
  lineCountOf,
} from '../../lib/fileMarks';
import type { GitFileMarks } from '../../lib/types';
import { marksGutter } from './gitMarksGutter';

// Viewer theme: match the chat's small mono text instead of the editor
// defaults (which inherit the UI base and look bulky). Both values are
// rem-based so Settings > Interface scales them with the rest of the UI.
const viewerTheme = EditorView.theme({
  '&': {
    height: '100%',
    // 0.8571rem = 12px at the 14px design base.
    fontSize: '0.8571rem',
    color: 'var(--color-fg)',
  },
  '.cm-scroller': {
    fontFamily: 'var(--oc-font-mono)',
    lineHeight: '1.55',
    overflow: 'auto',
  },
  '.cm-content': {
    padding: '10px 0',
  },
  '.cm-line': {
    padding: '0 16px',
  },
  '.cm-gutters': {
    backgroundColor: 'transparent',
    borderRight: 'none',
    color: 'var(--color-dim)',
    paddingLeft: '10px',
  },
  '.cm-activeLine': {
    backgroundColor: 'transparent',
  },
  '.cm-activeLineGutter': {
    backgroundColor: 'transparent',
  },
  '.cm-cursor': {
    borderLeftColor: 'var(--color-accent)',
  },
  '.cm-selectionBackground, &.cm-focused .cm-selectionBackground': {
    backgroundColor: 'color-mix(in srgb, var(--color-accent) 28%, transparent)',
  },
});

function languageFor(name: string): Extension | undefined {
  const ext = name.split('.').pop()?.toLowerCase() ?? '';
  switch (ext) {
    case 'go':
      return StreamLanguage.define(go);
    case 'ts':
    case 'tsx':
    case 'mts':
    case 'cts':
      return javascript({ jsx: true, typescript: true });
    case 'js':
    case 'jsx':
    case 'mjs':
    case 'cjs':
      return javascript({ jsx: true });
    case 'py':
      return python();
    case 'rs':
      return StreamLanguage.define(rust);
    case 'java':
      return java();
    case 'c':
    case 'h':
      return cpp();
    case 'cpp':
    case 'cc':
    case 'hpp':
      return cpp();
    case 'cs':
      return cpp();
    case 'html':
    case 'htm':
      return html();
    case 'css':
      return css();
    case 'scss':
    case 'less':
      return css();
    case 'json':
      return json();
    case 'yaml':
    case 'yml':
      return StreamLanguage.define(yaml);
    case 'xml':
    case 'svg':
      return xml();
    case 'sql':
      return sql();
    case 'sh':
    case 'bash':
    case 'zsh':
      return StreamLanguage.define(shell);
    case 'md':
    case 'markdown':
      return markdown();
    case 'toml':
      return StreamLanguage.define(toml);
    default:
      return undefined;
  }
}

export function CodePane({
  text,
  name,
  sourceView = false,
  onSource,
  marks = null,
}: {
  text: string;
  name: string;
  sourceView?: boolean;
  onSource?: () => void;
  // marks draws the git change gutter. Null keeps the pane bare, which
  // is what the non-git hosts (the preview dialog) pass.
  marks?: GitFileMarks | null;
}) {
  const { t } = useTranslation();
  const lang = useMemo(() => languageFor(name), [name]);
  const light = document.documentElement.classList.contains('theme-light');

  // The gutter is derived from the text on screen, so a range the
  // binding reported for a newer revision can never paint a wrong line.
  const layout = useMemo(
    () => classifyMarks(marks, lineCountOf(text)),
    [marks, text],
  );
  const layoutKey = useMemo(() => layoutSignature(layout), [layout]);
  const marksExt = useMemo(
    () =>
      marksGutter(layout, (tone, delTop, delBottom) => {
        const parts: string[] = [];
        if (tone === 'add') parts.push(t('files.marksAddedLine'));
        if (tone === 'mod') parts.push(t('files.marksModifiedLine'));
        if (delTop + delBottom > 0) {
          parts.push(t('files.deletedLines', { count: delTop + delBottom }));
        }
        return parts.join(' · ');
      }),
    // layoutKey stands in for the layout: a new marks payload that
    // classifies to the same lines must not reconfigure the editor.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [layoutKey, t],
  );
  const extensions = useMemo(() => {
    const list: Extension[] = [];
    if (lang) list.push(lang);
    list.push(viewerTheme, ...marksExt);
    return list;
  }, [lang, marksExt]);

  // A silent reload replaces the text in place; the reader's scroll
  // position has to survive it. The scroller is CodeMirror's own, so
  // its offset is captured as the user scrolls and restored after the
  // document was swapped.
  const hostRef = useRef<HTMLDivElement | null>(null);
  const scrolled = useRef({ top: 0, left: 0 });
  const lastText = useRef(text);
  // A passive effect, not a layout one: CodeMirror swaps the document in
  // its own effect, and React flushes a child's effects before the
  // parent's, so this already sees the new text in place.
  useEffect(() => {
    const scroller = hostRef.current?.querySelector('.cm-scroller');
    if (!scroller) return;
    if (lastText.current === text) return;
    lastText.current = text;
    scroller.scrollTop = scrolled.current.top;
    scroller.scrollLeft = scrolled.current.left;
  }, [text]);

  return (
    <div className="flex h-full min-h-0 flex-col">
      {sourceView && onSource && (
        <button
          onClick={onSource}
          className="m-2 flex w-fit items-center gap-1 self-end rounded-tight border border-edge bg-panel2 px-2 py-1 text-xs text-dim hover:text-fg"
        >
          <Eye size={ICON.xs} />
          {t('files.previewRendered')}
        </button>
      )}
      <div
        ref={hostRef}
        className="oc-editor-host min-h-0 flex-1 overflow-hidden"
        onScrollCapture={(event) => {
          const el = event.target as HTMLElement;
          if (!el.classList.contains('cm-scroller')) return;
          scrolled.current = { top: el.scrollTop, left: el.scrollLeft };
        }}
      >
        <CodeMirror
          value={text}
          height="100%"
          theme={light ? 'light' : 'dark'}
          extensions={extensions}
          readOnly
          basicSetup={{
            foldGutter: true,
            highlightActiveLine: false,
            highlightActiveLineGutter: false,
          }}
        />
      </div>
    </div>
  );
}
