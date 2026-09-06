import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { Markdown } from './Markdown';

describe('Markdown', () => {
  it('does not render raw HTML', () => {
    render(
      <Markdown
        text={
          'hello\n\n<script>window.pwned = 1</script>\n\n<img src=x onerror="pwn()">'
        }
      />,
    );
    expect(document.querySelector('script')).not.toBeInTheDocument();
    expect(document.querySelector('img')).not.toBeInTheDocument();
    expect(screen.getByText('hello')).toBeInTheDocument();
  });

  it('blocks javascript: links', () => {
    render(<Markdown text="[click me](javascript:alert(1))" />);
    // react-markdown drops unsafe destinations entirely: no link is
    // rendered, so a javascript: href can never reach the DOM.
    expect(
      screen.queryByRole('link', { name: 'click me' }),
    ).not.toBeInTheDocument();
  });

  it('keeps links inert when no handler is provided', () => {
    render(<Markdown text="[deploy.md](references/deploy.md)" />);

    const link = screen.getByRole('link', { name: 'deploy.md' });
    expect(link).toHaveAttribute('href', 'references/deploy.md');

    fireEvent.click(link);

    expect(screen.getByRole('link', { name: 'deploy.md' })).toBeInTheDocument();
  });

  it('routes link clicks through onOpen without navigating', () => {
    const onOpen = vi.fn();
    render(
      <Markdown
        text="[web](https://example.com) and [local](refs/guide.md)"
        onOpen={onOpen}
      />,
    );

    fireEvent.click(screen.getByRole('link', { name: 'web' }));
    expect(onOpen).toHaveBeenCalledWith('https://example.com', undefined);

    fireEvent.click(screen.getByRole('link', { name: 'local' }));
    expect(onOpen).toHaveBeenLastCalledWith('refs/guide.md', undefined);

    // The webview never navigated: the anchors are still mounted.
    expect(screen.getByRole('link', { name: 'web' })).toBeInTheDocument();
  });

  it('renders code fences with GFM', () => {
    const { container } = render(
      <Markdown
        text={
          '```ts\nconst x: number = 1;\n```\n\n| a | b |\n|---|---|\n| 1 | 2 |'
        }
      />,
    );
    expect(container.textContent).toContain('const x: number = 1;');
    expect(screen.getByRole('table')).toBeInTheDocument();
  });
});
