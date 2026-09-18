import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import type { InteractDTO } from '../lib/types';
import { InteractionCard } from './InteractionCard';

function spec(overrides: Partial<InteractDTO> = {}): InteractDTO {
  return {
    id: 'p-1',
    run_id: 'r-1',
    conversation_id: 's-1',
    kind: 'select',
    severity: 'notice',
    title: 'Allow running rm -rf?',
    body: [{ type: 'text', text: 'Command is not allowed' }],
    options: [{ label: 'Deny', value: 'deny' }],
    multi: false,
    allow_other: false,
    source: 'test',
    ...overrides,
  };
}

describe('InteractionCard severity', () => {
  it('wears the danger chrome for a sandbox escalation', () => {
    const { container } = render(
      <InteractionCard spec={spec({ severity: 'danger' })} />,
    );
    const card = container.firstElementChild as HTMLElement;
    expect(card.className).toContain('border-err/50');
    expect(card.querySelector('.text-err')).not.toBeNull();
  });

  it('wears the quiet accent chrome for a plain question', () => {
    const { container } = render(
      <InteractionCard
        spec={spec({ severity: 'info', kind: 'text', options: [] })}
      />,
    );
    const card = container.firstElementChild as HTMLElement;
    expect(card.className).toContain('border-accent/40');
  });

  it('falls back to the notice chrome when the severity is unknown', () => {
    // A payload the host never sends cannot make a decision look like
    // small talk.
    const { container } = render(
      <InteractionCard spec={spec({ severity: 'urgent' as never })} />,
    );
    const card = container.firstElementChild as HTMLElement;
    expect(card.className).toContain('border-warn/40');
  });

  it('renders a prompt whose body is null instead of throwing', () => {
    // Go marshals a nil body slice as null.
    render(<InteractionCard spec={spec({ body: null as never })} />);
    expect(screen.getByText('Allow running rm -rf?')).toBeInTheDocument();
  });
});
