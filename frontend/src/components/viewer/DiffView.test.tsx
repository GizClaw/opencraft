import { render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { PatchFileDTO } from '../../lib/types';
import { GitDiffView } from './DiffView';

const longLine =
  'const next = (await api.workspaces()) ?? []; // one comment that runs well past the card width on purpose';

function file(overrides: Partial<PatchFileDTO> = {}): PatchFileDTO {
  return {
    path: 'frontend/src/lib/store.ts',
    action: 'update',
    added: 1,
    removed: 1,
    lines: [
      { kind: 'delete', old_num: 12, new_num: 0, text: 'const stale = true;' },
      { kind: 'add', old_num: 0, new_num: 12, text: longLine },
    ],
    ...overrides,
  };
}

function textCell(text: string): HTMLElement {
  const node = screen.getByText(text);
  return node as HTMLElement;
}

describe('GitDiffView', () => {
  it('wraps long lines under the code column when wrap is set', () => {
    const { container } = render(<GitDiffView files={[file()]} wrap />);
    const cell = textCell(longLine);
    expect(cell.className).toContain('whitespace-pre-wrap');
    expect(cell.className).toContain('break-words');
    expect(cell.className).not.toContain('whitespace-pre ');
    // Sideways scrolling is what puts code outside the card; a wrapping
    // view must not offer it.
    expect(container.querySelector('.overflow-x-hidden')).not.toBeNull();
    expect(container.querySelector('.overflow-x-auto')).toBeNull();
    // The marker keeps its own column so wrapped text aligns with the
    // code, not with the +/- glyph.
    expect(
      container.querySelector('div.text-ok.select-none')?.textContent,
    ).toBe('+');
  });

  it('keeps one line per source line and sizes rows to their content without wrap', () => {
    const { container } = render(<GitDiffView files={[file()]} />);
    expect(textCell(longLine).className).toContain('whitespace-pre');
    // w-max + min-w-full extends the row tint under the text that is
    // scrolled to, instead of ending at the viewport edge.
    expect(container.querySelector('.min-w-full')).not.toBeNull();
    expect(container.querySelector('.overflow-x-auto')).not.toBeNull();
    expect(container.querySelector('.overflow-x-hidden')).toBeNull();
  });

  it('marks how each file changed and totals it in the header', () => {
    const { container } = render(
      <GitDiffView
        files={[
          file({ action: 'add', path: 'src/new.ts' }),
          file({ action: 'delete', path: 'src/old.ts', lines: [] }),
        ]}
      />,
    );
    expect(screen.getByText('src/new.ts')).toBeInTheDocument();
    expect(container.querySelectorAll('.text-ok').length).toBeGreaterThan(0);
    expect(container.querySelectorAll('.text-err').length).toBeGreaterThan(0);
    // A file whose lines are gone (replayed patch against the applied
    // file) explains the empty body instead of rendering a bare header.
    expect(
      screen.getByText('No line detail for this file'),
    ).toBeInTheDocument();
  });

  it('keeps the scroll box as the root for panel surfaces', () => {
    const { container } = render(
      <GitDiffView files={[file()]} maxHeight="h-full" />,
    );
    const root = container.firstElementChild as HTMLElement;
    // Panels size the viewport itself (h-full against a definite-height
    // parent); a wrapper would collapse that sizing.
    expect(root.className).toContain('h-full');
    expect(root.className).toContain('overflow-y-auto');
    expect(root.querySelector('.overflow-y-auto')).toBeNull();
  });

  it('nests an expandable view so its footer sits outside the scroll box', () => {
    const { container } = render(
      <GitDiffView files={[file()]} wrap framed={false} expandable />,
    );
    const root = container.firstElementChild as HTMLElement;
    expect(root.className).not.toContain('overflow-y-auto');
    expect(root.querySelector('.overflow-y-auto')).not.toBeNull();
  });

  it('paints a scrim under the fold instead of masking the scroll box', () => {
    // jsdom reports zero scroll metrics, so the overflow probe needs
    // real numbers to fire.
    const originals = {
      scrollHeight: Object.getOwnPropertyDescriptor(
        Element.prototype,
        'scrollHeight',
      )!,
      clientHeight: Object.getOwnPropertyDescriptor(
        Element.prototype,
        'clientHeight',
      )!,
    };
    Object.defineProperty(HTMLElement.prototype, 'scrollHeight', {
      configurable: true,
      get: () => 400,
    });
    Object.defineProperty(HTMLElement.prototype, 'clientHeight', {
      configurable: true,
      get: () => 120,
    });
    try {
      const { container } = render(
        <GitDiffView files={[file()]} wrap framed={false} expandable />,
      );
      const scrim = container.querySelector('.diff-fade-bottom');
      expect(scrim).not.toBeNull();
      // A mask on a box that grows by thousands of pixels on expand is
      // the repaint hazard this scrim replaced.
      const box = container.querySelector('.overflow-y-auto') as HTMLElement;
      expect(box.style.maskImage).toBe('');
      expect(screen.getByText('Show full diff')).toBeInTheDocument();
    } finally {
      Object.defineProperty(
        HTMLElement.prototype,
        'scrollHeight',
        originals.scrollHeight,
      );
      Object.defineProperty(
        HTMLElement.prototype,
        'clientHeight',
        originals.clientHeight,
      );
    }
  });

  it('renders both sections of a rewritten file without duplicate keys', () => {
    const errors = vi.spyOn(console, 'error').mockImplementation(() => {});
    const { container } = render(
      <GitDiffView
        files={[
          file({ action: 'delete', path: 'src/app.tsx', added: 0, removed: 1 }),
          file({ action: 'add', path: 'src/app.tsx', added: 1, removed: 0 }),
        ]}
      />,
    );
    // Same path twice: both file headers and both hunks must survive.
    expect(screen.getAllByText('src/app.tsx')).toHaveLength(2);
    expect(container.querySelectorAll('.grid').length).toBe(4);
    expect(errors).not.toHaveBeenCalled();
    errors.mockRestore();
  });
});
