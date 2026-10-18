import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { useEffect, useState } from 'react';
import { beforeAll, describe, expect, it, vi } from 'vitest';
import i18n from '../i18n';
import { ErrorBoundary } from './ErrorBoundary';

// The boundary reports every render error; the telemetry binding is not
// what this test is about.
vi.mock(
  '../../bindings/github.com/GizClaw/opencraft/internal/adapters/desktop/bindings/diagnostics',
  () => ({ ReportFrontendError: vi.fn(async () => {}) }),
);

// React logs every caught render error, and the boundary prints through
// the console it captured when it was imported: silence both before the
// imports run so the output stays readable.
vi.hoisted(() => {
  console.error = () => {};
});

function AlwaysThrows(): never {
  throw new Error('payload could not be parsed');
}

function Body({ fail, onMount }: { fail: boolean; onMount: () => void }) {
  useEffect(() => {
    onMount();
  }, [onMount]);
  if (fail) throw new Error('payload could not be parsed');
  return <span>recovered</span>;
}

// A surface that clears the data it failed on: without onRetry the retry
// would render the same payload and throw again.
function RetryableSurface({ onMount }: { onMount: () => void }) {
  const [fail, setFail] = useState(true);
  return (
    <ErrorBoundary inline scope="retry" onRetry={() => setFail(false)}>
      <Body fail={fail} onMount={onMount} />
    </ErrorBoundary>
  );
}

// The boundary renders through i18n directly (no hook), so the language
// is pinned before the first render: a late init would leave the card
// showing raw keys while the query looks for translated ones.
beforeAll(async () => {
  await i18n.changeLanguage('en');
});

const tryAgain = () =>
  screen.queryByRole('button', { name: i18n.t('app.tryAgain') });
const reload = () => screen.getByRole('button', { name: i18n.t('app.reload') });

// F-48: a surface that throws on render used to replace the whole window
// with the crash card, because the only boundary sat at the root.
describe('ErrorBoundary inline', () => {
  it('replaces only the surface that failed', () => {
    render(
      <div>
        <span>sidebar survived</span>
        <ErrorBoundary inline scope="chat">
          <AlwaysThrows />
        </ErrorBoundary>
      </div>,
    );

    expect(screen.getByText('sidebar survived')).toBeInTheDocument();
    expect(screen.getByText(i18n.t('app.surfaceFailed'))).toBeInTheDocument();
    // The compact card offers both ways out without the window-sized
    // crash card.
    expect(screen.queryByText(i18n.t('app.crashed'))).not.toBeInTheDocument();
  });

  it('retries by dropping the data the surface failed on', async () => {
    const user = userEvent.setup();
    const onMount = vi.fn();
    render(<RetryableSurface onMount={onMount} />);

    expect(screen.getByText(i18n.t('app.surfaceFailed'))).toBeInTheDocument();
    await user.click(tryAgain()!);

    expect(screen.getByText('recovered')).toBeInTheDocument();
    expect(onMount).toHaveBeenCalledTimes(1);
  });

  it('puts the card in the frame the surface owned', () => {
    const { container } = render(
      <ErrorBoundary inline scope="settings" frame="fixed inset-0 grid">
        <AlwaysThrows />
      </ErrorBoundary>,
    );

    // The settings page fills the window; a bare card would land in the
    // column the page was covering.
    expect(container.querySelector('.fixed.inset-0')).not.toBeNull();
    expect(screen.getByText(i18n.t('app.surfaceFailed'))).toBeInTheDocument();
  });

  it('stops offering a retry once the same failure repeats', async () => {
    const user = userEvent.setup();
    render(
      <ErrorBoundary inline scope="graph">
        <AlwaysThrows />
      </ErrorBoundary>,
    );

    // A deterministic error: two more attempts cannot change the data.
    await user.click(tryAgain()!);
    await user.click(tryAgain()!);

    expect(tryAgain()).not.toBeInTheDocument();
    expect(reload()).toBeInTheDocument();
  });
});
