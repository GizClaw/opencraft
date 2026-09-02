import { memo, useState } from 'react';
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
