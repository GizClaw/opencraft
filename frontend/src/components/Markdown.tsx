import { memo, useEffect, useState } from 'react';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import rehypeHighlight from 'rehype-highlight';
import { Check, Copy } from 'lucide-react';

// Markdown renders assistant content with GFM. Code blocks get a copy
// button, syntax highlighting via rehype-highlight, and tables are
// wrapped so they scroll instead of overflowing the chat column.
export const Markdown = memo(function Markdown({ text }: { text: string }) {
  return (
    <ReactMarkdown
      remarkPlugins={[remarkGfm]}
      rehypePlugins={[rehypeHighlight]}
      components={{
        pre: CodeBlock,
        table: (props) => (
          <div className="overflow-x-auto">
            <table {...props} />
          </div>
        ),
      }}
    >
      {text}
    </ReactMarkdown>
  );
});

// liveMarkdownCap keeps the throttled streaming preview cheap: beyond
// this size we fall back to plain text until the turn completes.
const liveMarkdownCap = 6000;

// MARKDOWN_RE is a cheap structural probe used during streaming: when
// the partial text has none of these markers, plain text is cheaper and
// visually identical.
const MARKDOWN_RE =
  /```|(^|\n)\s{0,3}#{1,6}\s|\*\*|__|^\s{0,3}([-+*]|\d+\.)\s|\|.*\|/m;

export function looksLikeMarkdown(text: string): boolean {
  return text.length <= liveMarkdownCap && MARKDOWN_RE.test(text);
}

// LiveMarkdown renders a still-streaming message as markdown, throttled
// so the unified parse+highlight pipeline runs at most every 120ms
// instead of once per token.
export const LiveMarkdown = memo(function LiveMarkdown({
  text,
}: {
  text: string;
}) {
  const [rendered, setRendered] = useState('');
  useEffect(() => {
    const timer = setTimeout(() => setRendered(text), 120);
    return () => clearTimeout(timer);
  }, [text]);
  return <Markdown text={rendered || text} />;
});

function CodeBlock({
  children,
  ...props
}: React.HTMLAttributes<HTMLPreElement>) {
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
      <pre {...props}>{children}</pre>
      {text && (
        <button
          onClick={() => void copy()}
          className="codeblock-copy"
          aria-label="Copy code"
        >
          {copied ? <Check size="0.7857rem" /> : <Copy size="0.7857rem" />}
          {copied ? 'Copied' : 'Copy'}
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
