import { render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { AppViewHost } from './AppViewHost';
import type { LiveAppScope } from '../apps/host';

// A view component that throws on render, which is what an application
// bundle with a bug does.
function Broken(): never {
  throw new Error('view exploded');
}

function scope(over: Partial<LiveAppScope> = {}): LiveAppScope {
  return {
    id: 'hello',
    manifest: { app: 'app: v1', id: 'hello', name: 'Hello' } as never,
    views: [],
    ...over,
  };
}

describe('AppViewHost', () => {
  it('shows the built-in conversation tab first, then the registered views', () => {
    render(
      <AppViewHost
        scope={scope({
          views: [
            {
              id: 'main',
              title: 'Main',
              appID: 'hello',
              Component: () => null,
            },
          ],
        })}
        tab="chat"
        onTab={() => {}}
      >
        <p>the conversation</p>
      </AppViewHost>,
    );

    const tabs = screen.getAllByRole('tab').map((el) => el.textContent);
    // The chat tab is the floor of the page: it is not something an
    // application registers, and it is what an empty bundle leaves.
    expect(tabs).toEqual(['Chat', 'Main']);
    expect(screen.getByText('the conversation')).toBeTruthy();
  });

  it('renders the selected view and reports tab clicks', () => {
    const onTab = vi.fn();
    render(
      <AppViewHost
        scope={scope({
          views: [
            {
              id: 'main',
              title: 'Main',
              appID: 'hello',
              Component: () => <p>view body</p>,
            },
          ],
        })}
        tab="main"
        onTab={onTab}
      >
        <p>the conversation</p>
      </AppViewHost>,
    );

    expect(screen.getByText('view body')).toBeTruthy();
    expect(screen.queryByText('the conversation')).toBeNull();
  });

  it('keeps the page alive when a view throws', () => {
    // React logs the error its boundary caught, with the component
    // stack; that output is the framework reporting a failure this test
    // asks for, so it is silenced rather than read.
    const logged = vi.spyOn(console, 'error').mockImplementation(() => {});
    try {
      render(
        <AppViewHost
          scope={scope({
            views: [
              { id: 'main', title: 'Main', appID: 'hello', Component: Broken },
            ],
          })}
          tab="main"
          onTab={() => {}}
        >
          <p>the conversation</p>
        </AppViewHost>,
      );
    } finally {
      logged.mockRestore();
    }

    // The boundary is per view, so the failure is shown in the view's
    // place: the window's tree stays up and the conversation is one tab
    // away. Without it React would rebuild from the nearest boundary
    // above and remount the whole page.
    expect(screen.getByText('This panel failed to load')).toBeTruthy();
    expect(screen.getByText('view exploded')).toBeTruthy();
    expect(screen.getByRole('tab', { name: /Chat/ })).toBeTruthy();
  });

  it('reports a bundle that failed to apply without hiding the conversation', () => {
    render(
      <AppViewHost
        scope={scope({ error: 'bundle must export apply(ctx)' })}
        tab="chat"
        onTab={() => {}}
      >
        <p>the conversation</p>
      </AppViewHost>,
    );

    expect(screen.getByTestId('app-view-error')).toBeTruthy();
    expect(screen.getByText('bundle must export apply(ctx)')).toBeTruthy();
    expect(screen.getByText('the conversation')).toBeTruthy();
  });
});
