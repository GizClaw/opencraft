import { fireEvent, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it } from 'vitest';
import { ActivityCard, type ActivityThink } from './ActivityCard';
import type { PlanPanelState } from '../lib/plan';
import type { SandboxProcess } from '../lib/types';

const plan: PlanPanelState = {
  plan: {
    items: [
      { step: 'Read code', status: 'completed' },
      { step: 'Rewrite', status: 'pending' },
    ],
  },
  live: false,
};

const think: ActivityThink = {
  id: 'think-1',
  text: 'The plan should cover the loader too.',
  live: false,
};

function proc(over: Partial<SandboxProcess> = {}): SandboxProcess {
  return {
    process_id: 'p-1',
    argv: ['npm', 'run', 'dev'],
    tty: false,
    pid: 42,
    started_at: '2026-01-01T00:00:00Z',
    running: true,
    tail: '',
    truncated: false,
    seq: 10,
    ...over,
  };
}

describe('ActivityCard', () => {
  it('renders nothing when no section has content', () => {
    const { container } = render(
      <ActivityCard plan={null} think={null} processes={[]} />,
    );
    expect(container).toBeEmptyDOMElement();
  });

  it('shows the plan with its progress and steps', () => {
    render(<ActivityCard plan={plan} think={null} processes={[]} />);
    expect(screen.getByText('1/2')).toBeInTheDocument();
    expect(screen.getByText('Read code')).toBeInTheDocument();
    expect(screen.getByText('Rewrite')).toBeInTheDocument();
  });

  it('offers no dismissal: the card lives as long as its activity', () => {
    render(<ActivityCard plan={plan} think={think} processes={[]} />);
    expect(screen.queryByRole('button', { name: 'Close' })).toBeNull();
  });

  it('folds the whole card to its header in one click', () => {
    render(
      <ActivityCard
        plan={plan}
        think={{ ...think, live: true }}
        processes={[proc({ tail: 'vite v7 building…\n' })]}
      />,
    );
    fireEvent.click(screen.getByTestId('activity-card-header'));

    // The header is the whole card now: the sections are gone, the dot
    // and the title are not — a folded card still says work is happening.
    expect(screen.getByTestId('activity-card')).toHaveAttribute(
      'data-folded',
      'true',
    );
    expect(screen.getByTestId('activity-card-header')).toHaveAttribute(
      'aria-expanded',
      'false',
    );
    expect(screen.getByText('Activity')).toBeInTheDocument();
    expect(screen.queryByTestId('plan-section')).toBeNull();
    expect(screen.queryByTestId('think-body')).toBeNull();
    expect(screen.queryByTestId('process-section')).toBeNull();
    // Still live, and saying so: the dot outlives the body.
    expect(screen.getByTestId('activity-card')).toHaveAttribute(
      'data-live',
      'true',
    );
  });

  it('keeps the card folded while the work goes on', () => {
    const { rerender } = render(
      <ActivityCard plan={plan} think={think} processes={[]} />,
    );
    fireEvent.click(screen.getByTestId('activity-card-header'));

    // A plan revision, a new thought and a new process all arrive without
    // re-opening it, the way a section behaves.
    rerender(
      <ActivityCard
        plan={{ ...plan, live: true }}
        think={{ id: 'think-2', text: 'Now the loader.', live: true }}
        processes={[proc({ tail: 'one\n' })]}
      />,
    );
    expect(screen.getByTestId('activity-card-header')).toHaveAttribute(
      'aria-expanded',
      'false',
    );
    expect(screen.queryByText('Read code')).toBeNull();
    expect(screen.queryByTestId('think-body')).toBeNull();
    expect(screen.queryByTestId('process-tail')).toBeNull();
  });

  it('unfolds on a second click, with the sections at their own defaults', () => {
    render(
      <ActivityCard
        plan={plan}
        think={{ ...think, live: true }}
        processes={[]}
      />,
    );
    // A fold made inside the body (here the thought) does not survive the
    // card being folded around it: a folded card renders no body to keep
    // it in, so unfolding remounts the sections the way a new card
    // mounts them.
    fireEvent.click(screen.getByTestId('think-section-header'));
    expect(screen.queryByTestId('think-body')).toBeNull();

    fireEvent.click(screen.getByTestId('activity-card-header'));
    fireEvent.click(screen.getByTestId('activity-card-header'));

    expect(screen.getByTestId('activity-card-header')).toHaveAttribute(
      'aria-expanded',
      'true',
    );
    expect(screen.getByText('Read code')).toBeInTheDocument();
    expect(screen.getByTestId('think-body')).toBeInTheDocument();
  });

  it('takes the fold from the keyboard, being a real button', async () => {
    const user = userEvent.setup();
    render(<ActivityCard plan={plan} think={null} processes={[]} />);

    // The header is the card's first stop in the tab order, and the fold
    // is the button's own activation: Enter folds, Space unfolds.
    await user.tab();
    expect(screen.getByTestId('activity-card-header')).toHaveFocus();
    await user.keyboard('{Enter}');
    expect(screen.queryByText('Read code')).toBeNull();

    await user.keyboard(' ');
    expect(screen.getByText('Read code')).toBeInTheDocument();
  });

  it('shows the last thought expanded while the model reasons', () => {
    render(
      <ActivityCard
        plan={null}
        think={{ ...think, live: true }}
        processes={[]}
      />,
    );
    expect(screen.getByText('Thinking…')).toBeInTheDocument();
    expect(
      screen.getByText('The plan should cover the loader too.'),
    ).toBeInTheDocument();
  });

  it('keeps the thought open once reasoning ends', () => {
    const { rerender } = render(
      <ActivityCard
        plan={null}
        think={{ ...think, live: true }}
        processes={[]}
      />,
    );
    expect(screen.getByTestId('think-body')).toBeInTheDocument();

    rerender(<ActivityCard plan={null} think={think} processes={[]} />);
    // Reasoning ending is the model moving on, not the reader: the
    // section reports it (the ellipsis and the spinner go) and the block
    // stays where it is, readable.
    expect(screen.getByText('Thinking')).toBeInTheDocument();
    expect(screen.getByTestId('think-body')).toHaveTextContent(think.text);
    expect(screen.getByTestId('think-section-header')).toHaveAttribute(
      'aria-expanded',
      'true',
    );
  });

  it('shows a thought that starts after the card mounted', () => {
    const { rerender } = render(
      <ActivityCard plan={plan} think={null} processes={[]} />,
    );
    expect(screen.queryByTestId('think-body')).toBeNull();

    // The card mounts with the turn and the turn's first token arrives
    // later: the block opens rather than landing behind a fold the reader
    // never made.
    rerender(
      <ActivityCard
        plan={plan}
        think={{ id: 'think-2', text: 'Weighing the layouts.', live: true }}
        processes={[]}
      />,
    );
    expect(screen.getByTestId('think-body')).toHaveTextContent(
      'Weighing the layouts.',
    );
  });

  it('keeps a thought the reader folded folded when another block streams', () => {
    const { rerender } = render(
      <ActivityCard
        plan={null}
        think={{ ...think, live: true }}
        processes={[]}
      />,
    );
    fireEvent.click(screen.getByTestId('think-section-header'));
    expect(screen.queryByTestId('think-body')).toBeNull();

    // A second reasoning block: new content, and the section stays as the
    // reader left it.
    rerender(
      <ActivityCard
        plan={null}
        think={{ id: 'think-2', text: 'Now the loader.', live: true }}
        processes={[]}
      />,
    );
    expect(screen.queryByTestId('think-body')).toBeNull();
    expect(screen.getByTestId('think-section-header')).toHaveAttribute(
      'aria-expanded',
      'false',
    );
  });

  it('folds the checklist away once every step is done', () => {
    const { rerender } = render(
      <ActivityCard plan={plan} think={null} processes={[]} />,
    );
    expect(screen.getByTestId('plan-section-header')).toHaveAttribute(
      'aria-expanded',
      'true',
    );

    rerender(
      <ActivityCard
        plan={{
          plan: {
            items: [
              { step: 'Read code', status: 'completed' },
              { step: 'Rewrite', status: 'completed' },
            ],
          },
          live: false,
        }}
        think={null}
        processes={[]}
      />,
    );
    expect(screen.getByTestId('plan-section-header')).toHaveAttribute(
      'aria-expanded',
      'false',
    );
    expect(screen.queryByText('Read code')).toBeNull();
  });

  it('keeps a plan the reader folded folded when the checklist moves', () => {
    const { rerender } = render(
      <ActivityCard plan={plan} think={null} processes={[]} />,
    );
    fireEvent.click(screen.getByTestId('plan-section-header'));
    expect(screen.queryByText('Rewrite')).toBeNull();

    // A revision arrives with a step finished; the fold state stays put
    // instead of re-expanding under the reader.
    rerender(
      <ActivityCard
        plan={{ ...plan, live: true }}
        think={null}
        processes={[]}
      />,
    );
    expect(screen.queryByText('Read code')).toBeNull();
    expect(screen.getByTestId('plan-section-header')).toHaveAttribute(
      'aria-expanded',
      'false',
    );
  });

  it('shows a running process with the tail of the selected one', () => {
    render(
      <ActivityCard
        plan={null}
        think={null}
        processes={[proc({ tail: 'vite v7 building…\n' })]}
      />,
    );
    expect(screen.getByText('npm run dev')).toBeInTheDocument();
    expect(screen.getByText('running')).toBeInTheDocument();
    expect(screen.getByText(/vite v7 building/)).toBeInTheDocument();
  });

  it('drops a process the moment it stops', () => {
    const { rerender } = render(
      <ActivityCard
        plan={null}
        think={null}
        processes={[proc({ tail: 'VITE ready\n' })]}
      />,
    );
    expect(screen.getByTestId('process-tail')).toHaveTextContent('VITE ready');

    // The same process, now exited. The card reports live work, so the
    // row leaves at the next read — and with it the tail and the exit
    // status, which are the transcript's to keep.
    rerender(
      <ActivityCard
        plan={null}
        think={null}
        processes={[
          proc({
            tail: 'VITE ready\n',
            running: false,
            exit_code: 0,
            exit_reason: 'exited',
          }),
        ]}
      />,
    );
    expect(screen.queryByTestId('process-section')).toBeNull();
    // Nothing else was happening, so the whole overlay is gone.
    expect(screen.queryByTestId('activity-card')).toBeNull();
  });

  it('follows the newest process when the selected one stops', () => {
    const older = proc({ process_id: 'p-old', tail: 'old server\n' });
    const newer = proc({
      process_id: 'p-new',
      argv: ['vitest', 'watch'],
      tail: 'new run\n',
    });
    const { rerender } = render(
      <ActivityCard plan={null} think={null} processes={[older, newer]} />,
    );
    // Rows read newest first, so the second one is the older process.
    fireEvent.click(screen.getAllByTestId('process-row')[1]);
    expect(screen.getByTestId('process-tail')).toHaveTextContent('old server');

    rerender(<ActivityCard plan={null} think={null} processes={[newer]} />);
    expect(screen.getAllByTestId('process-row')).toHaveLength(1);
    expect(screen.getByTestId('process-tail')).toHaveTextContent('new run');
  });

  it('keeps a process section the reader folded folded for a new process', () => {
    const { rerender } = render(
      <ActivityCard
        plan={null}
        think={null}
        processes={[proc({ tail: 'one\n' })]}
      />,
    );
    fireEvent.click(screen.getByTestId('process-section-header'));
    expect(screen.queryByTestId('process-tail')).toBeNull();

    rerender(
      <ActivityCard
        plan={null}
        think={null}
        processes={[
          proc({ tail: 'one\n' }),
          proc({ process_id: 'p-2', tail: 'two\n' }),
        ]}
      />,
    );
    expect(screen.queryByTestId('process-tail')).toBeNull();
    expect(screen.getByTestId('process-section-header')).toHaveAttribute(
      'aria-expanded',
      'false',
    );
  });

  it('marks a truncated tail', () => {
    render(
      <ActivityCard
        plan={null}
        think={null}
        processes={[proc({ tail: 'last line\n', truncated: true })]}
      />,
    );
    expect(screen.getByTestId('process-tail').textContent).toContain(
      'truncated',
    );
  });
});
