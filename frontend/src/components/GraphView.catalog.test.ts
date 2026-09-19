// Contract gate for the graph editor's field catalog.
//
// GraphView offers a curated list of config keys per node type. That list is
// hand-maintained, and the node scripts are the only readers: a key the node
// does not read is a silent no-op for whoever sets it in the panel (the
// editor suggests the field, the node ignores it), and a knob the node reads
// but the catalog lacks can only be reached through the raw JSON editor.
//
// This test pins both directions against the compaction node, whose knobs are
// the ones users actually tune (folding thresholds and budgets).
import { readFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';
import { NESTED_FIELDS } from './GraphView';

const REPO_ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '../../..');
const COMPACT_NODE = join(
  REPO_ROOT,
  'internal/foundation/config/assets/graphs/nodes/compact.js',
);

describe('graph editor config catalog', () => {
  const source = readFileSync(COMPACT_NODE, 'utf8');
  const offered = (NESTED_FIELDS['script.config'] ?? []).map(
    (spec) => spec.key,
  );

  it('offers only keys the compaction node reads', () => {
    const unread = offered.filter(
      (key) =>
        !source.includes(`cfg.${key}`) && !source.includes(`config.${key}`),
    );
    expect(unread).toEqual([]);
  });

  it('offers every knob the compaction node reads', () => {
    const read = new Set(
      [...source.matchAll(/cfg\.([a-z_]+)/g)].map((match) => match[1]),
    );
    const missing = [...read].filter((key) => !offered.includes(key));
    expect(missing).toEqual([]);
  });
});
