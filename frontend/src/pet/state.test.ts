import { describe, expect, it } from 'vitest';
import { toPetView, type PetStatePayload } from './state';

describe('toPetView', () => {
  it('defaults to an idle roaming pet without a snapshot', () => {
    expect(toPetView(null)).toEqual({
      phase: 'idle',
      disposition: 'roam',
      interactive: false,
      walking: false,
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
});
