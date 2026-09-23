import { createRef } from 'react';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
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

  it('routes Tab to onQueue and Enter to onSubmit outside popups', async () => {
    const ref = createRef<MarkdownComposerHandle>();
    const onSubmit = vi.fn();
    const onQueue = vi.fn(() => true);
    const user = userEvent.setup();
    render(
      <MarkdownComposer
        ref={ref}
        placeholder="Write…"
        onSubmit={onSubmit}
        onQueue={onQueue}
      />,
    );

    await user.type(screen.getByRole('textbox'), 'hello');
    await user.keyboard('{Tab}');
    expect(onQueue).toHaveBeenCalledTimes(1);

    await user.keyboard('{Enter}');
    expect(onSubmit).toHaveBeenCalledTimes(1);
  });

  it('stops the reply on Escape, but never takes the key from a popup', async () => {
    const onStop = vi.fn(() => true);
    const user = userEvent.setup();
    render(
      <MarkdownComposer
        placeholder="Write…"
        onSubmit={vi.fn()}
        onStop={onStop}
      />,
    );
    const box = screen.getByRole('textbox');

    // The composer owns Escape (the shell leaves it to text surfaces),
    // and a stop the handler performed is the composer's key to consume.
    const plain = fireEvent.keyDown(box, { key: 'Escape' });
    expect(onStop).toHaveBeenCalledTimes(1);
    expect(plain).toBe(false); // false = preventDefault was called

    // A mention popup is the exception: its keymap exits on Escape, so
    // the reply keeps running and the popup is the thing that closes.
    await user.type(box, 'see @');
    await screen.findByText('src/main.ts');
    await user.keyboard('{Escape}');
    expect(onStop).toHaveBeenCalledTimes(1);
    await waitFor(() => {
      expect(screen.queryByText('src/main.ts')).toBeNull();
    });
  });

  it('still sends on Enter inside a markdown wrapper', async () => {
    const onSubmit = vi.fn();
    const user = userEvent.setup();
    render(<MarkdownComposer placeholder="Write…" onSubmit={onSubmit} />);
    await user.type(screen.getByRole('textbox'), '- one');
    await user.keyboard('{Enter}');
    expect(onSubmit).toHaveBeenCalledTimes(1);
  });

  it('steps out of a list on Shift+Enter so the next line starts flush left', async () => {
    const ref = createRef<MarkdownComposerHandle>();
    const user = userEvent.setup();
    render(<MarkdownComposer ref={ref} placeholder="Write…" />);
    const box = screen.getByRole('textbox');

    await user.type(box, '- one');
    expect(box.querySelector('li')).not.toBeNull();

    // The first break opens a second line inside the bullet …
    await user.keyboard('{Shift>}{Enter}{/Shift}');
    expect(box.querySelector('li p')?.textContent).toBe('one');

    // … and the next one steps out of the list: what follows is a plain
    // paragraph at the left margin, not another indented line.
    await user.keyboard('{Shift>}{Enter}{/Shift}');
    await user.type(box, 'plain');
    const paragraph = [...box.querySelectorAll('p')].find(
      (node) => node.textContent === 'plain',
    );
    expect(paragraph).toBeDefined();
    expect(paragraph?.closest('li')).toBeNull();
    expect(box.querySelector('li')?.textContent).toBe('one');
  });

  it('drops the markdown wrapper on Backspace at the start of a line', async () => {
    const ref = createRef<MarkdownComposerHandle>();
    const user = userEvent.setup();
    render(<MarkdownComposer ref={ref} placeholder="Write…" />);
    const box = screen.getByRole('textbox');

    await user.type(box, '- one');
    await user.keyboard('{Home}{Backspace}');
    expect(box.querySelector('ul')).toBeNull();
    expect(box.querySelector('p')?.textContent).toBe('one');
    expect(ref.current?.getMarkdown()).not.toContain('- ');

    ref.current?.clear();
    await user.type(box, '> quoted');
    expect(box.querySelector('blockquote')).not.toBeNull();
    await user.keyboard('{Home}{Backspace}');
    expect(box.querySelector('blockquote')).toBeNull();
    expect(ref.current?.getMarkdown()).toContain('quoted');
    expect(ref.current?.getMarkdown()).not.toContain('>');

    ref.current?.clear();
    await user.type(box, '# title');
    expect(box.querySelector('h1')).not.toBeNull();
    await user.keyboard('{Home}{Backspace}');
    expect(box.querySelector('h1')).toBeNull();
    expect(ref.current?.getMarkdown()).toContain('title');
  });

  it('leaves Backspace alone away from the start of a wrapped line', async () => {
    const ref = createRef<MarkdownComposerHandle>();
    const user = userEvent.setup();
    render(<MarkdownComposer ref={ref} placeholder="Write…" />);
    const box = screen.getByRole('textbox');

    await user.type(box, '- one');
    await user.keyboard('{End}{Backspace}');
    expect(box.querySelector('li')).not.toBeNull();
    expect(ref.current?.getMarkdown()).toContain('- on');
  });

  it('keeps Shift+Enter a soft break outside markdown wrappers', async () => {
    const user = userEvent.setup();
    render(<MarkdownComposer placeholder="Write…" />);
    const box = screen.getByRole('textbox');

    await user.type(box, 'first line');
    await user.keyboard('{Shift>}{Enter}{/Shift}');
    expect(box.querySelector('p br')).not.toBeNull();
  });

  it('surfaces pasted image files through onPasteImages', () => {
    const onPasteImages = vi.fn();
    render(
      <MarkdownComposer
        placeholder="Write…"
        onSubmit={vi.fn()}
        onPasteImages={onPasteImages}
      />,
    );
    const file = new File(['png-bytes'], 'clip.png', { type: 'image/png' });
    const clipboardItem = {
      kind: 'file',
      type: 'image/png',
      getAsFile: () => file,
    };
    const clipboardData = {
      items: [clipboardItem],
      files: [file],
      types: ['Files'],
      getData: () => '',
      setData: () => undefined,
      clearData: () => undefined,
    } as unknown as DataTransfer;
    fireEvent.paste(screen.getByRole('textbox'), { clipboardData });

    expect(onPasteImages).toHaveBeenCalledWith([file]);
  });
});
