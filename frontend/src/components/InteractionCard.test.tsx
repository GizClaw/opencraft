import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { useRef } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { useStore } from '../lib/store';
import { useOverlayLayer } from '../lib/overlay';
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

const ALLOW = { label: 'Allow once', value: 'allow_once' };
const DENY = { label: 'Deny', value: 'deny' };

describe('InteractionCard keyboard', () => {
  const replyInteract = vi.fn(async () => undefined);

  beforeEach(() => {
    replyInteract.mockClear();
    useStore.setState({ replyInteract });
  });

  it('takes the keyboard when the prompt arrives', () => {
    // The host is blocked on this prompt; the mouse should not be
    // required to reach it.
    render(<InteractionCard spec={spec({ options: [ALLOW, DENY] })} />);
    expect(screen.getByLabelText('Allow once')).toHaveFocus();
  });

  it('stays passive while another layer owns the keyboard', async () => {
    // A prompt can arrive while the palette or a settings dialog is
    // open. Taking the caret then would send the user's next keystrokes
    // into a card they cannot see; the card waits for the layer and
    // takes the keyboard as soon as it closes.
    function FakeLayer({ open }: { open: boolean }) {
      const panelRef = useRef<HTMLDivElement | null>(null);
      useOverlayLayer({
        active: open,
        containerRef: panelRef,
        trap: false,
        lock: false,
        restoreFocus: false,
      });
      return open ? <div ref={panelRef} data-testid="layer" /> : null;
    }
    const view = render(
      <>
        <FakeLayer open />
        <InteractionCard spec={spec({ options: [ALLOW, DENY] })} />
      </>,
    );
    const choice = screen.getByLabelText('Allow once');
    expect(choice).not.toHaveFocus();
    view.rerender(
      <>
        <FakeLayer open={false} />
        <InteractionCard spec={spec({ options: [ALLOW, DENY] })} />
      </>,
    );
    await waitFor(() => expect(choice).toHaveFocus());
  });

  it('answers the choice the user made on Enter', () => {
    render(<InteractionCard spec={spec({ options: [ALLOW, DENY] })} />);
    const deny = screen.getByLabelText('Deny');
    fireEvent.click(deny);
    fireEvent.keyDown(deny, { key: 'Enter' });
    expect(replyInteract).toHaveBeenCalledTimes(1);
    expect(replyInteract).toHaveBeenCalledWith('p-1', {
      text: '',
      option: 'deny',
      options: undefined,
    });
  });

  it('leaves a bare Enter unanswered', () => {
    // Nothing is preselected, so Enter can never approve a prompt by
    // itself.
    render(<InteractionCard spec={spec({ options: [ALLOW, DENY] })} />);
    fireEvent.keyDown(screen.getByLabelText('Allow once'), { key: 'Enter' });
    expect(replyInteract).not.toHaveBeenCalled();
  });

  it('submits a typed answer from the answer field', () => {
    render(<InteractionCard spec={spec({ kind: 'text', options: [] })} />);
    const field = screen.getByPlaceholderText('Type your answer…');
    expect(field).toHaveFocus();
    fireEvent.change(field, { target: { value: 'use the staging bucket' } });
    fireEvent.keyDown(field, { key: 'Enter' });
    expect(replyInteract).toHaveBeenCalledWith('p-1', {
      text: 'use the staging bucket',
      option: null,
      options: undefined,
    });
  });

  it('leaves Shift+Enter to the answer field', () => {
    render(<InteractionCard spec={spec({ kind: 'text', options: [] })} />);
    const field = screen.getByPlaceholderText('Type your answer…');
    fireEvent.change(field, { target: { value: 'first line' } });
    fireEvent.keyDown(field, { key: 'Enter', shiftKey: true });
    expect(replyInteract).not.toHaveBeenCalled();
  });

  it('ignores the Enter that confirms an IME candidate', () => {
    render(<InteractionCard spec={spec({ kind: 'text', options: [] })} />);
    const field = screen.getByPlaceholderText('Type your answer…');
    fireEvent.change(field, { target: { value: '提交' } });
    fireEvent.keyDown(field, { key: 'Enter', isComposing: true });
    // WebKit reports the composing Enter as keyCode 229 instead.
    fireEvent.keyDown(field, { key: 'Enter', keyCode: 229 });
    expect(replyInteract).not.toHaveBeenCalled();
  });

  it('sends a custom answer typed into the other field', () => {
    render(
      <InteractionCard
        spec={spec({ options: [ALLOW, DENY], allow_other: true })}
      />,
    );
    const other = screen.getByPlaceholderText('Other (custom input)…');
    fireEvent.change(other, { target: { value: 'only for this run' } });
    fireEvent.keyDown(other, { key: 'Enter' });
    expect(replyInteract).toHaveBeenCalledWith('p-1', {
      text: 'only for this run',
      option: null,
      options: undefined,
    });
  });

  it('lets the buttons keep their own Enter', () => {
    // Enter on a button clicks it; submitting here as well would send
    // the prompt twice.
    render(<InteractionCard spec={spec({ kind: 'text', options: [] })} />);
    fireEvent.change(screen.getByPlaceholderText('Type your answer…'), {
      target: { value: 'hi' },
    });
    fireEvent.keyDown(screen.getByRole('button', { name: 'Submit' }), {
      key: 'Enter',
    });
    expect(replyInteract).not.toHaveBeenCalled();
  });

  it('hands the keyboard back once answered', () => {
    const onAnswered = vi.fn();
    render(
      <InteractionCard
        spec={spec({ kind: 'text', options: [] })}
        onAnswered={onAnswered}
      />,
    );
    const field = screen.getByPlaceholderText('Type your answer…');
    fireEvent.change(field, { target: { value: 'done' } });
    fireEvent.keyDown(field, { key: 'Enter' });
    expect(onAnswered).toHaveBeenCalledTimes(1);
  });
});
