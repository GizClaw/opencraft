import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { markdownStats, setPerfMetricsEnabled } from '../lib/perfMetrics';
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

  it('keeps react-markdown internals out of the DOM', () => {
    // react-markdown hands every component the hast `node` next to the
    // DOM props. Spreading it onto <pre>/<table> leaks a prop the DOM
    // never asked for - React 19 writes it out as an attribute.
    const warn = vi.spyOn(console, 'error').mockImplementation(() => {});
    const { container } = render(
      <Markdown
        text={'```ts\nconst x = 1;\n```\n\n| a | b |\n|---|---|\n| 1 | 2 |'}
      />,
    );

    const pre = container.querySelector('pre');
    expect(pre).not.toBeNull();
    expect(pre!.hasAttribute('node')).toBe(false);
    expect(screen.getByRole('table').hasAttribute('node')).toBe(false);
    expect(
      warn.mock.calls.map((call) => String(call[0])).join('\n'),
    ).not.toMatch(/`node` prop/);

    warn.mockRestore();
  });

  it('records one render-to-commit measurement for the probe', () => {
    // The markdown series is what a "render streaming markdown as plain
    // text" decision would be based on, so the pair has to bracket this
    // render: the body runs before react-markdown parses, the layout
    // effect after the block committed. The clock advances on every read
    // so the measurement cannot collapse to zero.
    let tick = 0;
    const nowSpy = vi
      .spyOn(performance, 'now')
      .mockImplementation(() => (tick += 10));
    setPerfMetricsEnabled(true);
    try {
      render(<Markdown text={'# hello\n\nsome **bold** text'} />);
      expect(markdownStats().max).toBeGreaterThan(0);
    } finally {
      setPerfMetricsEnabled(false);
      nowSpy.mockRestore();
    }
  });
});
