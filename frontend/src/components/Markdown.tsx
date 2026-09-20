import { memo, useState } from 'react';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import rehypeHighlight from 'rehype-highlight';
import { Check, Copy } from 'lucide-react';
import { ICON } from './ui/icon';

// MarkdownLinkHandler is the shape every markdown surface hands its
// anchors: the href plus the base path (directory of the document
// being rendered) the handler resolves it against.
export type MarkdownLinkHandler = (href: string, basePath?: string) => void;

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

function CodeBlock(
  props: React.HTMLAttributes<HTMLPreElement> & { node?: unknown },
) {
  const [copied, setCopied] = useState(false);
  const text = extractText(props.children);
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
      <pre {...withoutNode(props)} />
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
