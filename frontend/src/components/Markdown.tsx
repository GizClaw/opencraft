import {
  createContext,
  memo,
  useContext,
  useLayoutEffect,
  useState,
} from 'react';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import rehypeHighlight from 'rehype-highlight';
import { Check, Copy } from 'lucide-react';
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

// withoutNode strips the hast `node` react-markdown hands every component
// alongside the DOM props. Spreading it onto a real element writes the node
// itself into the markup (React 19 renders `node="[object Object]"`).
function withoutNode<T extends { node?: unknown }>(props: T): Omit<T, 'node'> {
  const { node, ...rest } = props;
  void node;
  return rest;
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
  return (
    <ReactMarkdown
      remarkPlugins={[remarkGfm]}
      rehypePlugins={[rehypeHighlight]}
      components={{
        a: ({ href, children }) => (
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
        ),
        code: (props) => (
          <InlineCode {...props} onOpen={onOpen} basePath={basePath} />
        ),
        pre: CodeBlock,
        table: (props) => (
          <div className="overflow-x-auto">
            <table {...withoutNode(props)} />
          </div>
        ),
      }}
    >
      {text}
    </ReactMarkdown>
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
  onOpen,
  basePath,
}: {
  node?: unknown;
  className?: string;
  children?: React.ReactNode;
  onOpen?: MarkdownLinkHandler;
  basePath?: string;
}) {
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
