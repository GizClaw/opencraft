import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it } from 'vitest';
import { ContextMeter } from './ContextMeter';
import type { ContextUsageDTO } from '../lib/types';

// The meter is presentational: the conversation carries the snapshot,
// the backend decides the numbers. What it has to get right is that the
// breakdown names every slice it draws, that the shares are of the same
// total the trigger prints, and that a prompt with no known window still
// says something true instead of inventing a denominator.
const usage: ContextUsageDTO = {
  measured_tokens: 41000,
  limit_tokens: 200000,
  model: 'claude-sonnet-4',
  categories: [
    { id: 'instructions', tokens: 6000 },
    { id: 'environment', tokens: 1500 },
    { id: 'project_docs', tokens: 500 },
    { id: 'user_memory', tokens: 1000 },
    { id: 'memory_summary', tokens: 0 },
    { id: 'conversation', tokens: 22000 },
    { id: 'turn_context', tokens: 2000 },
    { id: 'tools', tokens: 8000 },
  ],
};

const open = async () => {
  const user = userEvent.setup();
  await user.click(screen.getByRole('button', { name: 'Context usage' }));
  return screen.getByRole('dialog', { name: 'Context usage' });
};

describe('ContextMeter', () => {
  it('collapsed it reads as a fraction of the model window', () => {
    render(<ContextMeter usage={usage} />);
    const trigger = screen.getByRole('button', { name: 'Context usage' });
    expect(trigger).toHaveTextContent('41k');
    expect(trigger).toHaveTextContent('/ 200k');
    expect(trigger).toHaveAttribute('aria-expanded', 'false');
  });

  it('opened it lists every category once, in canonical order', async () => {
    render(<ContextMeter usage={usage} />);
    const dialog = await open();
    const rows = within(dialog).getAllByRole('listitem');
    expect(rows.map((r) => r.textContent)).toEqual([
      'System prompt6k15%',
      'Environment and permissions1.5k3.7%',
      'Project docs5001.2%',
      'User memory1k2.4%',
      'Conversation22k54%',
      'Turn context2k4.9%',
      'Tools and overhead8k20%',
    ]);
    // The summary contributed nothing this turn, so it is not a row: an
    // empty slice would be a legend entry for a segment nobody can see.
    expect(within(dialog).queryByText('Memory summary')).toBeNull();
  });

  it('the header states the share of the window the prompt took', async () => {
    render(<ContextMeter usage={usage} />);
    const dialog = await open();
    expect(within(dialog).getByText(/of 200k/)).toBeInTheDocument();
    expect(within(dialog).getByText('claude-sonnet-4')).toBeInTheDocument();
  });

  it('a prompt past the window says how far past it ran', async () => {
    render(
      <ContextMeter
        usage={{
          ...usage,
          measured_tokens: 0,
          limit_tokens: 10000,
          categories: [{ id: 'conversation', tokens: 42400 }],
        }}
      />,
    );
    const dialog = await open();
    // The slice percentages share the prompt and stop at 100%, but this
    // one divides by the window and can pass it. Rounding 424% down to
    // "100%" would draw four windows of prompt as exactly one.
    expect(within(dialog).getByText('424% of 10k')).toBeInTheDocument();
  });

  it('a category this build does not know is counted but not drawn', async () => {
    render(
      <ContextMeter
        usage={{
          ...usage,
          categories: [
            { id: 'instructions', tokens: 1000 },
            { id: 'future_slice', tokens: 1000 },
          ],
        }}
      />,
    );
    const dialog = await open();
    // Both slices are in the total the trigger prints...
    expect(
      screen.getByRole('button', { name: 'Context usage' }),
    ).toHaveTextContent('2k');
    // ...but only the known one has a name to put on a row.
    expect(within(dialog).getAllByRole('listitem')).toHaveLength(1);
    expect(within(dialog).getByText('System prompt')).toBeInTheDocument();
  });

  it('a deployment that declares no window shows the count alone', async () => {
    render(
      <ContextMeter
        usage={{ ...usage, measured_tokens: 0, limit_tokens: undefined }}
      />,
    );
    expect(
      screen.getByRole('button', { name: 'Context usage' }),
    ).toHaveTextContent('41k');
    expect(
      screen.getByRole('button', { name: 'Context usage' }),
    ).not.toHaveTextContent('/');
    const dialog = await open();
    expect(within(dialog).getByText('used so far')).toBeInTheDocument();
  });

  it('a turn without a usage report says the numbers are estimates', async () => {
    render(<ContextMeter usage={{ ...usage, measured_tokens: 0 }} />);
    const dialog = await open();
    expect(
      within(dialog).getByText(/Estimated from this turn's prompt/),
    ).toBeInTheDocument();
  });

  it('a measured prompt carries no estimate disclaimer', async () => {
    render(<ContextMeter usage={usage} />);
    const dialog = await open();
    expect(within(dialog).queryByText(/Estimated from/)).toBeNull();
  });

  it('a conversation row that is a remainder says so', async () => {
    render(<ContextMeter usage={{ ...usage, conversation_capped: true }} />);
    const dialog = await open();
    expect(
      within(dialog).getByText(
        /Conversation is what the provider's count left/,
      ),
    ).toBeInTheDocument();
    // The total is still the measurement, so the estimate disclaimer does
    // not apply: the two footnotes answer different questions.
    expect(within(dialog).queryByText(/Estimated from/)).toBeNull();
  });

  it('a conversation that fits the measurement carries no remainder note', async () => {
    render(<ContextMeter usage={usage} />);
    const dialog = await open();
    expect(within(dialog).queryByText(/Conversation is what/)).toBeNull();
  });

  it('draws nothing before the conversation has been metered', () => {
    const { container } = render(
      <ContextMeter usage={{ measured_tokens: 0, categories: [] }} />,
    );
    expect(container).toBeEmptyDOMElement();
  });
});
