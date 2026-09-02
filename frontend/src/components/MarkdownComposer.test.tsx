import { createRef } from 'react';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import {
  MarkdownComposer,
  type MarkdownComposerHandle,
} from './MarkdownComposer';

const apiMock = vi.hoisted(() => ({
  searchFiles: vi.fn(),
  skills: vi.fn(),
}));

vi.mock('../lib/api', () => ({ api: apiMock }));

describe('MarkdownComposer', () => {
  beforeEach(() => {
    apiMock.searchFiles.mockReset();
    apiMock.skills.mockReset();
    apiMock.searchFiles.mockResolvedValue([
      { path: 'src/main.ts', is_dir: false },
    ]);
    apiMock.skills.mockResolvedValue([
      {
        name: 'review',
        description: 'Review code',
        scope: 'workspace',
        path: 'x',
      },
    ]);
  });

  it('round-trips markdown and mention shortcodes as trigger text', async () => {
    const ref = createRef<MarkdownComposerHandle>();
    render(
      <MarkdownComposer
        ref={ref}
        placeholder="Write…"
        initialMarkdown="Hello **world**"
      />,
    );

    // The handle is available synchronously because the editor is created
    // during the first render.
    expect(ref.current).not.toBeNull();
    expect(ref.current?.getMarkdown()).toContain('**world**');

    ref.current?.setMarkdown(
      'See [mention id="src/main.ts" label="src/main.ts"] now',
    );
    const markdown = ref.current?.getMarkdown() ?? '';
    expect(markdown).toContain('@src/main.ts');
    expect(markdown).not.toContain('[mention');

    ref.current?.setMarkdown('```ts\nconst x = 1\n```');
    expect(ref.current?.getMarkdown()).toContain('```ts');
  });

  it('inserts an @ file suggestion as a highlighted mention', async () => {
    const ref = createRef<MarkdownComposerHandle>();
    const user = userEvent.setup();
    render(<MarkdownComposer ref={ref} placeholder="Write…" />);

    await user.type(screen.getByRole('textbox'), 'see @');
    await screen.findByText('src/main.ts');
    const popup = screen
      .getByText('src/main.ts')
      .closest('.suggestion-popup') as HTMLElement | null;
    expect(popup?.style.top).not.toBe('0px');
    await user.keyboard('{Enter}');

    await waitFor(() => {
      expect(ref.current?.getMarkdown()).toContain('@src/main.ts');
    });
    expect(apiMock.searchFiles).toHaveBeenCalledWith('');
  });
});
