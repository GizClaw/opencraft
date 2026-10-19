import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { markdownTrees } from '../lib/markdownCache';
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

  it('makes an inline code path clickable through onOpen', () => {
    const onOpen = vi.fn();
    render(
      <Markdown
        text={'read `internal/foundation/config/registry.go` first'}
        basePath="docs"
        onOpen={onOpen}
      />,
    );

    fireEvent.click(
      screen.getByRole('button', {
        name: 'internal/foundation/config/registry.go',
      }),
    );

    expect(onOpen).toHaveBeenCalledWith(
      'internal/foundation/config/registry.go',
      'docs',
    );
  });

  it('peels a line tag off a clickable code path', () => {
    const onOpen = vi.fn();
    render(<Markdown text={'see `store.ts:412`'} onOpen={onOpen} />);

    fireEvent.click(screen.getByRole('button', { name: 'store.ts:412' }));

    expect(onOpen).toHaveBeenCalledWith('store.ts', undefined);
  });

  it('leaves commands and identifiers as plain code', () => {
    const onOpen = vi.fn();
    render(
      <Markdown
        text={'run `npm test` then check `Object.keys` here'}
        onOpen={onOpen}
      />,
    );

    expect(screen.queryByRole('button')).not.toBeInTheDocument();
    expect(screen.getByText('npm test')).toBeInTheDocument();
    expect(screen.getByText('Object.keys')).toBeInTheDocument();
  });

  it('keeps fenced code inert however it is spelled', () => {
    // The block's `code` element goes through the same component map as
    // an inline span: only its `pre` wrapper knows the difference, and a
    // block has no single target to offer.
    const onOpen = vi.fn();
    const { container } = render(
      <Markdown text={'```\nsrc/lib/store.ts\n```'} onOpen={onOpen} />,
    );

    expect(
      screen.queryByRole('button', { name: 'src/lib/store.ts' }),
    ).not.toBeInTheDocument();
    // The block is still a block: its copy button is there.
    expect(
      screen.getByRole('button', { name: 'Copy code' }),
    ).toBeInTheDocument();
    expect(container.querySelector('pre')?.textContent).toContain(
      'src/lib/store.ts',
    );
  });

  it('mounts a block it parsed before without parsing it again', () => {
    // The transcript unmounts a row that scrolls out of its window and
    // mounts it again when it scrolls back. The parse is a pure function of
    // the source text, so the second mount reuses the tree the first one
    // built — and that tree still renders against the surface it is mounted
    // under, which is what lets the new mount route its links through its
    // own handler (lib/markdownCache).
    const text = 'read [the guide](refs/guide.md) first';
    const missesBefore = markdownTrees.misses;
    const first = render(<Markdown text={text} />);
    first.unmount();
    const hitsBefore = markdownTrees.hits;

    const onOpen = vi.fn();
    render(<Markdown text={text} onOpen={onOpen} />);

    expect(markdownTrees.misses - missesBefore).toBe(1);
    expect(markdownTrees.hits - hitsBefore).toBe(1);
    expect(screen.getByRole('link', { name: 'the guide' })).toHaveAttribute(
      'href',
      'refs/guide.md',
    );
    fireEvent.click(screen.getByRole('link', { name: 'the guide' }));
    expect(onOpen).toHaveBeenCalledWith('refs/guide.md', undefined);
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

  it('measures the parse, not the cache hit that replaces it', () => {
    // flush_md_p95 is the number the cache's argument is written in, so a
    // hit must not add a sample: it would pull the series towards zero as
    // the cache warms, and the parse it saves would stop showing up in the
    // one place it is measured.
    let tick = 0;
    const nowSpy = vi
      .spyOn(performance, 'now')
      .mockImplementation(() => (tick += 10));
    setPerfMetricsEnabled(true);
    const text = '# measured once\n\nsome **body** text';
    try {
      const first = render(<Markdown text={text} />);
      first.unmount();
      const parsed = markdownStats();
      expect(parsed.max).toBeGreaterThan(0);

      // A read taken on the hit path would be a hundred times the sample
      // above, which is what this makes visible.
      nowSpy.mockImplementation(() => (tick += 100_000));
      render(<Markdown text={text} />);

      expect(markdownStats()).toEqual(parsed);
    } finally {
      setPerfMetricsEnabled(false);
      nowSpy.mockRestore();
    }
  });
});
