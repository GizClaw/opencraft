// Cross-language gate for the compaction estimate.
//
// The graph's compaction node is a JS asset
// (internal/foundation/config/assets/graphs/nodes/compact.js) that
// decides whether to fold older rounds by estimating the MainChannel's
// prompt footprint. The Go side owns the same rules
// (internal/foundation/utils/summarytext): RenderMessage renders a
// message to its prompt form, EstimateTokens estimates a list. The node
// mirrors both in JS because a script node cannot call into Go yet
// (flowcraft#539), so this test runs the mirrored functions against the
// fixture the Go test generates and fails the moment the two drift.
//
// Regenerate the fixture with:
//   UPDATE_GOLDEN=1 go test ./internal/foundation/utils/summarytext/
import { readFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';
import { COMPACT_SUMMARY_PREFIX } from './compact';

const REPO_ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '../../..');
const NODE_SOURCE = join(
  REPO_ROOT,
  'internal/foundation/config/assets/graphs/nodes/compact.js',
);
const FIXTURE = join(
  REPO_ROOT,
  'internal/foundation/utils/summarytext/testdata/compact_cases.json',
);

interface CompactCase {
  name: string;
  messages: unknown[];
  rendered: string[];
  tokens: number;
}

interface CompactFixture {
  summaryPrefix: string;
  cases: CompactCase[];
}

/** Minimal board stub: the node reads the channel and a few board vars. */
function boardStub() {
  return {
    MAIN_CHANNEL: 'main',
    channel: () => [],
    getVar: () => 0,
    setVar: () => {},
    setChannel: () => {},
    appendChannel: () => {},
  };
}

// The node asset runs its top-level decision on load, so evaluating it
// needs every global it touches. Appending a return statement pulls the
// two mirrored functions out of the script scope.
function loadNodeFunctions(): {
  renderText: (m: unknown) => string;
  estimateTokens: (msgs: unknown[]) => number;
} {
  const source = readFileSync(NODE_SOURCE, 'utf8');
  const factory = new Function(
    'board',
    'config',
    'run',
    'inference',
    `${source}\nreturn { renderText, estimateTokens };`,
  );
  return factory(
    boardStub(),
    {},
    { get_context_id: () => 'test' },
    { routeExplain: () => ({ limits: { max_input_tokens: 0 } }) },
  );
}

describe('compact node mirrors the Go compaction estimate', () => {
  const fixture = JSON.parse(readFileSync(FIXTURE, 'utf8')) as CompactFixture;
  const { renderText, estimateTokens } = loadNodeFunctions();

  it('has fixtures to compare', () => {
    expect(fixture.cases.length).toBeGreaterThan(5);
  });

  // The marker is written by the Go compaction tool and recognized by
  // this frontend constant; both are hand-kept copies.
  it('shares the summary marker with the Go side', () => {
    expect(COMPACT_SUMMARY_PREFIX).toBe(fixture.summaryPrefix);
  });

  for (const testCase of fixture.cases) {
    it(`renders and estimates "${testCase.name}" like Go`, () => {
      expect(testCase.messages.map((m) => renderText(m))).toEqual(
        testCase.rendered,
      );
      expect(estimateTokens(testCase.messages)).toBe(testCase.tokens);
    });
  }
});
