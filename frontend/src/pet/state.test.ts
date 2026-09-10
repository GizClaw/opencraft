import { describe, expect, it } from 'vitest';
import { toPetView, type PetStatePayload } from './state';

describe('toPetView', () => {
  it('defaults to an idle roaming pet without a snapshot', () => {
    expect(toPetView(null)).toEqual({
      phase: 'idle',
      disposition: 'roam',
      interactive: false,
      walking: false,
      sleeping: false,
      intentSeq: 0,
    });
  });

  it('passes through a working snapshot with tool details', () => {
    const payload: PetStatePayload = {
      agent_id: 'assistant',
      phase: 'tool',
      tool_name: 'apply_patch',
      tool_category: 'file',
      disposition: 'work',
    };
    expect(toPetView(payload)).toEqual({
      phase: 'tool',
      disposition: 'work',
      toolName: 'apply_patch',
      toolCategory: 'file',
      interactive: false,
      walking: false,
      sleeping: false,
      intentSeq: 0,
    });
  });

  it('keeps asking interactive so the surface can opt into clicks', () => {
    const payload: PetStatePayload = {
      phase: 'asking',
      disposition: 'ask',
      interactive: true,
    };
    expect(toPetView(payload).interactive).toBe(true);
  });

  it('carries the sleep flag and the intent sequence', () => {
    const payload: PetStatePayload = {
      phase: 'idle',
      disposition: 'sleep',
      sleeping: true,
      intent: 'wave',
      intent_seq: 3,
    };
    const view = toPetView(payload);
    expect(view.sleeping).toBe(true);
    expect(view.intentSeq).toBe(3);
  });

  it('carries the walk direction and leaves it unset before the first step', () => {
    expect(toPetView(null).facing).toBeUndefined();
    expect(
      toPetView({
        phase: 'idle',
        disposition: 'roam',
        walking: true,
        facing: 'left',
      }).facing,
    ).toBe('left');
  });
});
