import { useMemo } from 'react';
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

// Viewer theme: match the chat's small mono text instead of the
// editor defaults (which inherit the 15.68px UI base and look bulky).
const viewerTheme = EditorView.theme({
  '&': {
    height: '100%',
    fontSize: '12px',
    color: 'var(--color-fg)',
  },
  '.cm-scroller': {
    fontFamily:
      'ui-monospace, SFMono-Regular, Menlo, Consolas, "PingFang SC", monospace',
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
}: {
  text: string;
  name: string;
  sourceView?: boolean;
  onSource?: () => void;
}) {
  const { t } = useTranslation();
  const lang = useMemo(() => languageFor(name), [name]);
  const light = document.documentElement.classList.contains('theme-light');
  return (
    <div className="flex h-full min-h-0 flex-col">
      {sourceView && onSource && (
        <button
          onClick={onSource}
          className="m-2 flex w-fit items-center gap-1 self-end rounded border border-edge bg-panel2 px-2 py-1 text-xs text-dim hover:text-fg"
        >
          <Eye size="0.7857rem" />
          {t('files.previewRendered')}
        </button>
      )}
      <div className="oc-editor-host min-h-0 flex-1 overflow-hidden">
        <CodeMirror
          value={text}
          height="100%"
          theme={light ? 'light' : 'dark'}
          extensions={lang ? [lang, viewerTheme] : [viewerTheme]}
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
