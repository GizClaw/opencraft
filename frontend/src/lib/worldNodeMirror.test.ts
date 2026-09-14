// Cross-language gate for the world node.
//
// Go renders the world state (instructions, environment, permissions,
// memory, AGENTS.md) into board vars
// (internal/capabilities/worldstate/worldstate.go: RenderToBoard), and
// the graph's world node replays them onto the MainChannel
// (internal/foundation/config/assets/graphs/nodes/world.js). The var
// names and the section JSON shape are a contract between the two, and
// a mismatch is silent: the model would lose its instructions, and the
// memory hooks that locate the user's turn on the channel would slice
// in the wrong place.
//
// The fixture carries what Go actually writes, so running the node
// against it checks the contract in both directions.
//
// Regenerate the fixture with:
//   UPDATE_GOLDEN=1 go test ./internal/capabilities/worldstate/
import { readFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';

const REPO_ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '../../..');
const NODE_SOURCE = join(
  REPO_ROOT,
  'internal/foundation/config/assets/graphs/nodes/world.js',
);
const FIXTURE = join(
  REPO_ROOT,
  'internal/capabilities/worldstate/testdata/world_cases.json',
);

interface BoardMessage {
  role: string;
  content: { parts: unknown[] };
}

interface WorldCase {
  name: string;
  sections: string;
  history?: string;
  existing: BoardMessage[];
}

interface WorldFixture {
  cases: WorldCase[];
}

/** Board stub recording what the node writes, seeded with Go's vars. */
function runWorldNode(testCase: WorldCase) {
  const vars: Record<string, unknown> = {
    'world.sections': testCase.sections,
  };
  if (testCase.history) vars['world.history'] = testCase.history;
  let channel: BoardMessage[] = testCase.existing;
  const board = {
    MAIN_CHANNEL: 'main',
    getVar: (key: string) => vars[key],
    setVar: (key: string, value: unknown) => {
      vars[key] = value;
    },
    channel: () => channel,
    setChannel: (_kind: string, next: BoardMessage[]) => {
      channel = next;
    },
  };
  new Function('board', readFileSync(NODE_SOURCE, 'utf8'))(board);
  return { vars, channel };
}

describe('world node mirrors the Go board payload', () => {
  const fixture = JSON.parse(readFileSync(FIXTURE, 'utf8')) as WorldFixture;

  it('has fixtures to compare', () => {
    expect(fixture.cases.length).toBeGreaterThan(1);
  });

  for (const testCase of fixture.cases) {
    it(`replays "${testCase.name}" onto the channel`, () => {
      const sections = JSON.parse(testCase.sections) as BoardMessage[];
      const history = JSON.parse(testCase.history ?? '[]') as BoardMessage[];
      const { vars, channel } = runWorldNode(testCase);

      // The counts the memory hooks read to separate injected context
      // from the user's own turn.
      expect(vars['world.sections.count']).toBe(sections.length);
      expect(vars['world.history.count']).toBe(history.length);

      // Sections first, then replayed history, then whatever the
      // channel already held.
      expect(channel).toEqual([
        ...sections.map(({ role, content }) => ({ role, content })),
        ...history.map(({ role, content }) => ({ role, content })),
        ...testCase.existing,
      ]);

      // Every seeded message must be a message the engine accepts: a
      // role plus at least one content part. A renamed field on the Go
      // side would produce undefined here.
      for (const msg of channel) {
        expect(typeof msg.role).toBe('string');
        expect(msg.role.length).toBeGreaterThan(0);
        expect(Array.isArray(msg.content?.parts)).toBe(true);
        expect(msg.content.parts.length).toBeGreaterThan(0);
      }
    });
  }
});
