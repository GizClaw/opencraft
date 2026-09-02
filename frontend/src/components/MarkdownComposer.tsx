import {
  forwardRef,
  useEffect,
  useImperativeHandle,
  useMemo,
  useRef,
  useState,
  type MutableRefObject,
} from 'react';
import Mention from '@tiptap/extension-mention';
import Placeholder from '@tiptap/extension-placeholder';
import { Markdown } from '@tiptap/markdown';
import {
  EditorContent,
  Extension,
  ReactRenderer,
  useEditor,
  type Editor,
  type JSONContent,
} from '@tiptap/react';
import StarterKit from '@tiptap/starter-kit';
import { Plugin, PluginKey, TextSelection } from '@tiptap/pm/state';
import { Decoration, DecorationSet } from '@tiptap/pm/view';
import type { Node as PMNode } from '@tiptap/pm/model';
import type { SuggestionProps } from '@tiptap/suggestion';
import { File, Folder, Loader2, Sparkles } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { api } from '../lib/api';
import type { SkillDTO } from '../lib/types';

type MentionKind = 'file' | 'skill';

interface MentionOption {
  id: string;
  label: string;
  sub?: string;
  isDir?: boolean;
}

interface MentionPopupProps {
  kind: MentionKind;
  items: MentionOption[];
  query: string;
  loading: boolean;
  command: (option: MentionOption) => void;
}

interface MentionPopupHandle {
  handleKeyDown(event: KeyboardEvent): boolean;
}

const MentionPopup = forwardRef<MentionPopupHandle, MentionPopupProps>(
  function MentionPopup({ kind, items, query, loading, command }, ref) {
    const { t } = useTranslation();
    const [active, setActive] = useState(0);
    const commandRef = useRef(command);
    const rootRef = useRef<HTMLDivElement>(null);
    useEffect(() => {
      commandRef.current = command;
    }, [command]);

    // A fresh query or result set starts the selection at the top again.
    useEffect(() => {
      setActive(0);
    }, [items, query, loading]);

    const choose = (index: number) => {
      const item = items[index];
      if (!item) return;
      commandRef.current?.({ id: item.id, label: item.label });
    };

    useImperativeHandle(
      ref,
      () => ({
        handleKeyDown(event) {
          if (event.isComposing || event.keyCode === 229) return false;
          if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
            event.preventDefault();
            if (items.length > 0) {
              const delta = event.key === 'ArrowDown' ? 1 : -1;
              setActive((current) => {
                const next = (current + delta + items.length) % items.length;
                const el = rootRef.current?.querySelector(
                  `[data-mention-index="${next}"]`,
                );
                el?.scrollIntoView({ block: 'nearest' });
                return next;
              });
            }
            return true;
          }
          if (event.key === 'Enter') {
            event.preventDefault();
            if (items.length > 0) choose(active);
            return true;
          }
          return false;
        },
      }),
      [active, items, choose],
    );

    const hint =
      kind === 'file'
        ? t('chat.mentionSearchHint')
        : t('chat.mentionSkillHint');
    const empty = query.length > 0;

    return (
      <div
        ref={rootRef}
        className="z-50 max-w-96 min-w-72 overflow-hidden rounded-lg border border-edge bg-panel2 shadow-2xl"
      >
        {loading && items.length === 0 ? (
          <div className="flex items-center gap-2 px-3 py-2 text-xs text-dim">
            <Loader2 size="0.8571rem" className="animate-spin text-accent" />
            {hint}
          </div>
        ) : items.length === 0 ? (
          <div className="px-3 py-2 text-xs text-dim">
            {empty ? t('chat.mentionNoMatch') : hint}
          </div>
        ) : (
          <div className="max-h-48 overflow-y-auto py-1">
            {items.map((item, index) => (
              <button
                key={item.id}
                data-mention-index={index}
                type="button"
                onMouseDown={(event) => event.preventDefault()}
                onClick={() => choose(index)}
                onMouseEnter={() => setActive(index)}
                className={`flex w-full items-center gap-2 px-3 py-1.5 text-left text-sm ${
                  index === active
                    ? 'bg-accent/15 text-accent'
                    : 'hover:bg-panel'
                }`}
              >
                {kind === 'file' ? (
                  item.isDir ? (
                    <Folder size="0.9286rem" className="shrink-0 text-accent" />
                  ) : (
                    <File size="0.9286rem" className="shrink-0 text-dim" />
                  )
                ) : (
                  <Sparkles size="0.9286rem" className="shrink-0 text-accent" />
                )}
                <span className="min-w-0 flex-1 truncate">{item.label}</span>
                {item.sub && (
                  <span className="truncate text-xs text-dim">{item.sub}</span>
                )}
              </button>
            ))}
          </div>
        )}
      </div>
    );
  },
);

function createMentionRenderer(
  kind: MentionKind,
  openRef: MutableRefObject<boolean>,
) {
  let component: ReactRenderer<MentionPopupHandle, MentionPopupProps> | null =
    null;
  let unmount: (() => void) | null = null;
  let latestProps: SuggestionProps<MentionOption> | null = null;

  const toProps = (
    props: SuggestionProps<MentionOption>,
  ): MentionPopupProps => ({
    kind,
    items: props.items,
    query: props.query,
    loading: props.loading,
    command: props.command,
  });

  // TipTap's suggestion anchor is a ProseMirror decoration. While an IME
  // composition is in flight the decoration DOM can be absent, and the
  // built-in positioning then falls back to a DOMRect at (0,0), which puts
  // the popup in the window's top-left corner. When that happens, anchor to
  // the editor's live caret coordinates instead.
  const anchorRect = (): DOMRect | null => {
    const props = latestProps;
    if (!props) return null;
    const fromDecoration = props.clientRect?.() ?? null;
    const isZero =
      fromDecoration &&
      fromDecoration.left === 0 &&
      fromDecoration.top === 0 &&
      fromDecoration.width === 0 &&
      fromDecoration.height === 0;
    if (fromDecoration && !isZero) return fromDecoration;
    try {
      const coords = props.editor.view.coordsAtPos(
        props.editor.state.selection.$anchor.pos,
      );
      if (
        !coords ||
        ![coords.left, coords.top, coords.right, coords.bottom].every(
          Number.isFinite,
        )
      ) {
        return null;
      }
      return new DOMRect(
        coords.left,
        coords.top,
        Math.max(1, coords.right - coords.left),
        Math.max(1, coords.bottom - coords.top),
      );
    } catch {
      return null;
    }
  };

  const positionPopup = () => {
    const element = component?.element;
    if (!element) return;
    const rect = anchorRect();
    if (!rect) {
      element.style.visibility = 'hidden';
      return;
    }
    const gap = latestProps?.offset?.mainAxis ?? 4;
    const width = element.offsetWidth || element.scrollWidth || 288;
    const height = element.offsetHeight || element.scrollHeight || 0;
    const placeBelow =
      rect.bottom + gap + height <= window.innerHeight - 8 ||
      rect.top - gap - height < 8;
    const top = placeBelow
      ? rect.bottom + gap
      : Math.max(8, rect.top - gap - height);
    const left = Math.min(
      Math.max(8, rect.left),
      Math.max(8, window.innerWidth - width - 8),
    );
    Object.assign(element.style, {
      position: 'fixed',
      left: `${left}px`,
      top: `${top}px`,
      visibility: 'visible',
    });
  };

  return {
    onBeforeStart() {
      openRef.current = true;
    },
    onStart(props: SuggestionProps<MentionOption>) {
      openRef.current = true;
      latestProps = props;
      component = new ReactRenderer(MentionPopup, {
        editor: props.editor,
        props: toProps(props),
        className: 'suggestion-popup',
      });
      component.element.style.visibility = 'hidden';
      component.element.style.width = 'max-content';
      unmount = props.mount(component.element, {
        onPosition: () => positionPopup(),
      });
    },
    onUpdate(props: SuggestionProps<MentionOption>) {
      latestProps = props;
      component?.updateProps(toProps(props));
      positionPopup();
    },
    onExit() {
      openRef.current = false;
      latestProps = null;
      unmount?.();
      unmount = null;
      component?.destroy();
      component = null;
    },
    onKeyDown({ event }: { event: KeyboardEvent }) {
      // Let the IME finish composing before the popup handles keys, or
      // Enter would swallow the candidate-confirmation keystroke.
      if (event.isComposing || event.keyCode === 229) return false;
      if (event.key === 'Enter') {
        component?.ref?.handleKeyDown(event);
        return true;
      }
      return component?.ref?.handleKeyDown(event) ?? false;
    },
  };
}

// Skills are small enough to load once per app run and filter locally.
let skillsCache: SkillDTO[] | null = null;
let skillsLoading: Promise<SkillDTO[]> | null = null;

function loadSkills(): Promise<SkillDTO[]> {
  if (skillsCache) return Promise.resolve(skillsCache);
  if (!skillsLoading) {
    skillsLoading = api
      .skills()
      .then((skills) => {
        skillsCache = Array.isArray(skills) ? skills : [];
        return skillsCache;
      })
      .catch(() => {
        skillsCache = [];
        return skillsCache;
      })
      .finally(() => {
        skillsLoading = null;
      });
  }
  return skillsLoading;
}

async function searchFileOptions(
  query: string,
  signal: AbortSignal,
): Promise<MentionOption[]> {
  try {
    const hits = await api.searchFiles(query);
    if (signal.aborted) return [];
    return (Array.isArray(hits) ? hits : []).slice(0, 12).map((hit) => ({
      id: hit.path,
      label: hit.path,
      isDir: hit.is_dir,
    }));
  } catch {
    return [];
  }
}

async function searchSkillOptions(
  query: string,
  signal: AbortSignal,
): Promise<MentionOption[]> {
  const skills = await loadSkills();
  if (signal.aborted) return [];
  const q = query.toLowerCase();
  return skills
    .filter((skill) => skill.name.toLowerCase().includes(q))
    .slice(0, 12)
    .map((skill) => ({
      id: skill.name,
      label: skill.name,
      sub: skill.description,
    }));
}

// The default TipTap mention serializer writes `[mention id="..."]`
// shortcodes; this override keeps the outgoing markdown exactly as the
// user sees it (`@path` / `$skill`).
const OpenCraftMention = Mention.extend({
  renderMarkdown(node: JSONContent) {
    const attrs = node.attrs as Record<string, unknown> | undefined;
    const trigger = attrs?.mentionSuggestionChar === '$' ? '$' : '@';
    const target = (attrs?.label ?? attrs?.id) as string | undefined;
    return `${trigger}${target ?? ''}`;
  },
});

// Keep the old composer's behavior of colouring @file/$skill tokens even
// when the user typed them without opening the suggestion picker. Mention
// atoms selected from the popup get the same class from their rendered HTML.
const MENTION_TOKEN_RE = /@[\w./-]+|\$[a-z0-9-]+/g;
const MentionHighlight = Extension.create({
  name: 'mentionHighlight',
  addProseMirrorPlugins() {
    const pluginKey = new PluginKey('mentionHighlight');
    const buildDecorations = (doc: PMNode) => {
      const decorations: Decoration[] = [];
      doc.descendants((node, pos) => {
        if (!node.isText || !node.text) return;
        if (node.marks.some((mark) => mark.type.name === 'code')) return;
        const re = new RegExp(MENTION_TOKEN_RE.source, 'g');
        let match: RegExpExecArray | null;
        while ((match = re.exec(node.text)) !== null) {
          decorations.push(
            Decoration.inline(
              pos + match.index,
              pos + match.index + match[0].length,
              { class: 'mention-token' },
            ),
          );
        }
      });
      return DecorationSet.create(doc, decorations);
    };
    return [
      new Plugin({
        key: pluginKey,
        state: {
          init: (_config, state) => buildDecorations(state.doc),
          apply: (transaction, _value, _oldState, newState) =>
            transaction.docChanged ? buildDecorations(newState.doc) : _value,
        },
        props: {
          decorations: (state) => pluginKey.getState(state) ?? null,
        },
      }),
    ];
  },
});

export interface MarkdownComposerHandle {
  getMarkdown(): string;
  setMarkdown(markdown: string): void;
  clear(): void;
  focus(): void;
}

interface MarkdownComposerProps {
  initialMarkdown?: string;
  placeholder: string;
  disabled?: boolean;
  onValueChange?: (markdown: string) => void;
  onSubmit?: () => void;
}

function serializeEditor(editor: Editor | null): string {
  if (!editor) return '';
  const markdown = editor.getMarkdown();
  // Belt-and-braces: if a mention ever escapes through the default
  // shortcode serializer (for example pasted content), turn it back into
  // the trigger text the user sees.
  return markdown.replace(/\[mention\s+([^\]]*)\]/g, (_whole, raw: string) => {
    const attrs: Record<string, string> = {};
    const re = /(\w+)=(?:"([^"]*)"|'([^']*)'|([^\s]+))/g;
    let m: RegExpExecArray | null;
    while ((m = re.exec(raw)) !== null) {
      attrs[m[1]] = m[2] ?? m[3] ?? m[4] ?? '';
    }
    const trigger = attrs.char === '$' ? '$' : '@';
    return `${trigger}${attrs.label || attrs.id || ''}`;
  });
}

export const MarkdownComposer = forwardRef<
  MarkdownComposerHandle,
  MarkdownComposerProps
>(function MarkdownComposer(
  {
    initialMarkdown = '',
    placeholder,
    disabled = false,
    onValueChange,
    onSubmit,
  },
  ref,
) {
  const placeholderRef = useRef(placeholder);

  const suggestionOpenRef = useRef(false);
  const onChangeRef = useRef(onValueChange);
  const onSubmitRef = useRef(onSubmit);
  useEffect(() => {
    onChangeRef.current = onValueChange;
  }, [onValueChange]);
  useEffect(() => {
    onSubmitRef.current = onSubmit;
  }, [onSubmit]);

  const extensions = useMemo(
    () => [
      StarterKit.configure({
        link: {
          autolink: true,
          linkOnPaste: true,
          openOnClick: false,
        },
      }),
      Markdown,
      MentionHighlight,
      Placeholder.configure({
        placeholder: () => placeholderRef.current,
        emptyEditorClass: 'is-editor-empty',
        emptyNodeClass: 'is-empty',
        showOnlyWhenEditable: false,
      }),
      OpenCraftMention.configure({
        HTMLAttributes: {
          class: 'mention-token',
        },
        suggestions: [
          {
            char: '@',
            debounce: 120,
            items: ({ query, signal }) => searchFileOptions(query, signal),
            render: () => createMentionRenderer('file', suggestionOpenRef),
            decorationClass: 'mention-suggestion',
          },
          {
            char: '$',
            debounce: 120,
            items: ({ query, signal }) => searchSkillOptions(query, signal),
            render: () => createMentionRenderer('skill', suggestionOpenRef),
            decorationClass: 'mention-suggestion',
          },
        ],
      }),
    ],
    [],
  );

  const editor = useEditor({
    extensions,
    content: initialMarkdown,
    contentType: 'markdown',
    editable: !disabled,
    autofocus: false,
    editorProps: {
      attributes: {
        class: 'markdown-editor',
        spellcheck: 'true',
        role: 'textbox',
        'aria-multiline': 'true',
      },
      handleKeyDown: (_view, event) => {
        // While a mention popup is open its own keymap owns Enter/arrows;
        // let the plugin handle them instead of submitting the message.
        if (suggestionOpenRef.current) return false;
        if (event.key === 'Home' || event.key === 'End') {
          event.preventDefault();
          const edge = event.key === 'Home' ? 'start' : 'end';
          const $from = _view.state.selection.$from;
          const target = edge === 'start' ? $from.start() : $from.end();
          let anchor = target;
          if (event.shiftKey) {
            const domSel = _view.dom.ownerDocument.getSelection();
            const domAnchor = domSel?.anchorNode
              ? _view.posAtDOM(domSel.anchorNode, domSel.anchorOffset)
              : null;
            anchor = domAnchor ?? _view.state.selection.anchor;
          }
          const tr = _view.state.tr.setSelection(
            TextSelection.create(_view.state.doc, anchor, target),
          );
          _view.dispatch(tr);
          return true;
        }
        if (
          event.key === 'Enter' &&
          !event.shiftKey &&
          !event.isComposing &&
          event.keyCode !== 229
        ) {
          const type = _view.state.selection.$from.parent.type;
          // Inside a code block Enter writes a new line; the send button
          // still submits normally.
          if (type.name === 'codeBlock') return false;
          event.preventDefault();
          onSubmitRef.current?.();
          return true;
        }
        return false;
      },
    },
    onUpdate: ({ editor: current }) => {
      onChangeRef.current?.(serializeEditor(current));
    },
  });

  useEffect(() => {
    if (placeholderRef.current === placeholder) return;
    placeholderRef.current = placeholder;
    // Refresh the placeholder decoration with a no-op transaction so the
    // text follows language switches even while the editor is empty.
    editor?.view.dispatch(editor.state.tr);
  }, [placeholder, editor]);

  useImperativeHandle(
    ref,
    () => ({
      getMarkdown: () => serializeEditor(editor),
      setMarkdown: (markdown) => {
        editor?.commands.setContent(markdown, {
          contentType: 'markdown',
          emitUpdate: false,
        });
      },
      clear: () => {
        editor?.commands.setContent('', {
          contentType: 'markdown',
          emitUpdate: false,
        });
        onChangeRef.current?.('');
      },
      focus: () => {
        editor?.commands.focus();
      },
    }),
    [editor],
  );

  // Keep the parent's send button state in sync even when the content is
  // replaced programmatically.
  useEffect(() => {
    onChangeRef.current?.(serializeEditor(editor));
  }, []);

  return (
    <div className="relative">
      <EditorContent
        editor={editor}
        className={`markdown-composer max-h-52 overflow-y-auto no-scrollbar px-4 text-sm leading-relaxed text-fg caret-accent outline-none ${
          disabled ? 'opacity-50' : ''
        }`}
      />
    </div>
  );
});
