// The pane reads one file per tab and keeps a background read for the
// stamps. Both are answered out of order the moment the reader switches
// tabs quickly, and the failure is silent: the body of the file they
// left renders under the tab they picked.
import { act, render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { FilePreview, FileTab } from '../../lib/types';
import { FilePreviewPane } from './FilePreviewPane';

const apiMock = vi.hoisted(() => ({ readPreview: vi.fn() }));
vi.mock('../../lib/api', () => ({ api: apiMock }));

function tab(over: Partial<FileTab> = {}): FileTab {
  return {
    key: 'k1',
    path: '/tmp/w/docs/one.md',
    rel: 'docs/one.md',
    root: 'workspace',
    name: 'one.md',
    media_type: 'text/markdown',
    ...over,
  };
}

function preview(text: string, over: Partial<FilePreview> = {}): FilePreview {
  return {
    path: '/tmp/w/docs/one.md',
    rel: 'docs/one.md',
    root: 'workspace',
    name: 'one.md',
    size: text.length,
    media_type: 'text/markdown',
    kind: 'text',
    text,
    ...over,
  };
}

describe('FilePreviewPane', () => {
  beforeEach(() => {
    apiMock.readPreview.mockReset();
  });

  it('keeps the file the reader moved to when the first read answers late', async () => {
    let releaseFirst: ((p: FilePreview) => void) | undefined;
    apiMock.readPreview.mockReturnValueOnce(
      new Promise<FilePreview>((resolve) => {
        releaseFirst = resolve;
      }),
    );
    const { rerender } = render(
      <FilePreviewPane tab={tab()} onOpen={() => {}} />,
    );

    apiMock.readPreview.mockResolvedValueOnce(
      preview('the second document', {
        path: '/tmp/w/docs/two.md',
        rel: 'docs/two.md',
        name: 'two.md',
      }),
    );
    rerender(
      <FilePreviewPane
        tab={tab({
          key: 'k2',
          path: '/tmp/w/docs/two.md',
          rel: 'docs/two.md',
          name: 'two.md',
        })}
        onOpen={() => {}}
      />,
    );
    expect(await screen.findByText('the second document')).toBeInTheDocument();

    // The first read was slow, not cancelled: it answers after the switch,
    // and its body belongs to a file the reader has left.
    await act(async () => {
      releaseFirst?.(preview('the first document'));
    });
    expect(screen.getByText('the second document')).toBeInTheDocument();
    expect(screen.queryByText('the first document')).toBeNull();
  });

  it('shows the read error of the tab it belongs to', async () => {
    apiMock.readPreview.mockRejectedValueOnce(new Error('gone'));
    render(<FilePreviewPane tab={tab()} onOpen={() => {}} />);
    expect(await screen.findByText('Error: gone')).toBeInTheDocument();
  });
});
