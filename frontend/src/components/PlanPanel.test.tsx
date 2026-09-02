import { render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { PlanSnapshot } from '../lib/plan';
import { PlanPanel } from './PlanPanel';

const plan: PlanSnapshot = {
  items: [
    { step: 'Read code', status: 'completed' },
    { step: 'Rewrite', status: 'pending' },
  ],
};

describe('PlanPanel', () => {
  it('shows progress and item steps', () => {
    render(<PlanPanel plan={plan} live={false} />);
    expect(screen.getByText('1/2')).toBeInTheDocument();
    expect(screen.getByText('Read code')).toBeInTheDocument();
    expect(screen.getByText('Rewrite')).toBeInTheDocument();
  });

  it('fires onClose', () => {
    const onClose = vi.fn();
    render(<PlanPanel plan={plan} live={false} onClose={onClose} />);
    screen.getByLabelText('Close').click();
    expect(onClose).toHaveBeenCalledOnce();
  });
});
