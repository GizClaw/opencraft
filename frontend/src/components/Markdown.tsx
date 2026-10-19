import {
  createContext,
  memo,
  useContext,
  useLayoutEffect,
  useMemo,
  useState,
  type ReactNode,
} from 'react';
import ReactMarkdown, { type Components } from 'react-markdown';
import remarkGfm from 'remark-gfm';
import rehypeHighlight from 'rehype-highlight';
import { Check, Copy } from 'lucide-react';
import { markdownTrees } from '../lib/markdownCache';
import { beginMarkdownRender, endMarkdownRender } from '../lib/perfMetrics';
import { codeLinkTarget } from '../lib/linkTarget';
import { ICON } from './ui/icon';

// MarkdownLinkHandler is the shape every markdown surface hands its
// anchors: the href plus the base path (directory of the document
// being rendered) the handler resolves it against.
export type MarkdownLinkHandler = (href: string, basePath?: string) => void;

// InCodeBlock tells an inline `code` element that it belongs to a fenced
// block. react-markdown builds the block's code element through the same
// component map, and only the surrounding `pre` (CodeBlock) knows the
// difference.
const InCodeBlock = createContext(false);

// MarkdownSurface carries the two props that differ per surface — the link
// handler and the base path — to the component map below. The map itself is
// a module constant, so react-markdown sees the same identity on every
// render and a host re-render does not re-parse and re-highlight the whole
// answer through freshly built elements.
const MarkdownSurface = createContext<{
  onOpen?: MarkdownLinkHandler;
  basePath?: string;
}>({});

// The plugin lists are constants for the same reason. rehype-raw is
// deliberately absent from them: model output is rendered as markdown, not
// as raw HTML.
const REMARK_PLUGINS = [remarkGfm];
const REHYPE_PLUGINS = [rehypeHighlight];

// withoutNode strips the hast `node` react-markdown hands every component
// alongside the DOM props. Spreading it onto a real element writes the node
// itself into the markup (React 19 renders `node="[object Object]"`).
function withoutNode<T extends { node?: unknown }>(props: T): Omit<T, 'node'> {
  const { node, ...rest } = props;
  void node;
  return rest;
}

// MarkdownAnchor routes every click through the surface's link resolver
// instead of letting the webview navigate.
function MarkdownAnchor({
  href,
  children,
}: {
  node?: unknown;
  href?: string;
  children?: React.ReactNode;
}) {
  const { onOpen, basePath } = useContext(MarkdownSurface);
  return (
    <a
      href={href}
      onClick={(event) => {
        event.preventDefault();
        if (onOpen && href) {
          onOpen(href, basePath);
        }
      }}
    >
      {children}
    </a>
  );
}

// MarkdownTable wraps tables so they scroll instead of overflowing the
// chat column.
function MarkdownTable(
  props: React.HTMLAttributes<HTMLTableElement> & { node?: unknown },
) {
  return (
    <div className="overflow-x-auto">
      <table {...withoutNode(props)} />
    </div>
  );
}

const MARKDOWN_COMPONENTS: Components = {
  a: MarkdownAnchor,
  code: InlineCode,
  pre: CodeBlock,
  table: MarkdownTable,
};

// markdownTree answers the elements for one block: the tree this window
// parsed from that exact text before, or a fresh one kept for the next
// mount (see lib/markdownCache for why it is kept and what bounds it).
//
// ReactMarkdown is a plain function of its options — no hooks — so calling
// it here is the parse the JSX below used to ask for, once per text instead
// of once per mount. The elements it returns are descriptors rendered
// against the current surface, which is what lets a cached tree mount under
// a different link handler without being rebuilt.
function markdownTree(text: string): ReactNode {
  const cached = markdownTrees.get(text);
  if (cached !== undefined) return cached;
  const tree = ReactMarkdown({
    children: text,
    remarkPlugins: REMARK_PLUGINS,
    rehypePlugins: REHYPE_PLUGINS,
    components: MARKDOWN_COMPONENTS,
  });
  markdownTrees.set(text, tree);
  return tree;
}

// Markdown renders assistant content with GFM. Code blocks get a copy
// button, syntax highlighting via rehype-highlight, and tables are
// wrapped so they scroll instead of overflowing the chat column.
// Anchors never navigate the webview: onOpen routes every click
// through the central link resolver (system browser for URLs, the file
// viewer for local targets). Callers that render markdown without a
// handler keep the anchors inert.
export const Markdown = memo(function Markdown({
  text,
  basePath,
  onOpen,
}: {
  text: string;
  basePath?: string;
  onOpen?: MarkdownLinkHandler;
}) {
  // The probe's markdown series wants the cost of one block, so the pair is
  // taken around this render: the body runs before react-markdown parses
  // and builds its elements, and the layout effect runs after the block has
  // committed. memo keeps a re-render of the parent from looking like a
  // parse, and a disabled probe hands back a zero the effect ignores.
  const renderStartedAt = beginMarkdownRender();
  useLayoutEffect(() => {
    endMarkdownRender(renderStartedAt);
  });
  // The context value is rebuilt only when the two props it carries change,
  // so the constant component map does not re-render its consumers either.
  const surface = useMemo(() => ({ onOpen, basePath }), [onOpen, basePath]);
  return (
    <MarkdownSurface.Provider value={surface}>
      {markdownTree(text)}
    </MarkdownSurface.Provider>
  );
});

// InlineCode renders one inline `code` span. An assistant writes paths in
// backticks as freely as it writes commands, and a reader who wants the
// file should not have to copy the path into a shell: a span that names
// exactly one target (see codeLinkTarget) becomes a button that hands it
// to the surface's link handler, and everything else stays text. Only the
// surrounding `pre` knows a code element belongs to a fenced block, so
// that travels in a context — a block has no single target to offer, and
// the highlighter has already replaced its text with spans.
function InlineCode({
  className,
  children,
}: {
  node?: unknown;
  className?: string;
  children?: React.ReactNode;
}) {
  const { onOpen, basePath } = useContext(MarkdownSurface);
  const inBlock = useContext(InCodeBlock);
  const target =
    inBlock || !onOpen ? null : codeLinkTarget(extractText(children));
  if (!target || !onOpen) {
    return <code className={className}>{children}</code>;
  }
  return (
    <code className={className}>
      <button
        type="button"
        onClick={() => onOpen(target, basePath)}
        // The chip keeps its code styling: only the hover affordance
        // says the path is a door.
        className="cursor-pointer text-left decoration-dotted underline-offset-2 hover:underline"
        data-tip={target}
      >
        {children}
      </button>
    </code>
  );
}

function CodeBlock(
  props: React.HTMLAttributes<HTMLPreElement> & { node?: unknown },
) {
  const { children, ...pre } = props;
  const [copied, setCopied] = useState(false);
  const text = extractText(children);
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(text);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // clipboard unavailable (e.g. non-secure context): ignore
    }
  };
  return (
    <div className="codeblock">
      <pre {...withoutNode(pre)}>
        <InCodeBlock.Provider value={true}>{children}</InCodeBlock.Provider>
      </pre>
      {text && (
        <button
          onClick={() => void copy()}
          className="codeblock-copy"
          aria-label="Copy code"
        >
          {copied ? <Check size={ICON.xs} /> : <Copy size={ICON.xs} />}
        </button>
      )}
    </div>
  );
}

function extractText(children: React.ReactNode): string {
  if (typeof children === 'string') return children;
  if (Array.isArray(children)) return children.map(extractText).join('');
  if (children && typeof children === 'object' && 'props' in children) {
    return extractText(
      (children as { props: { children?: React.ReactNode } }).props.children,
    );
  }
  return '';
}
