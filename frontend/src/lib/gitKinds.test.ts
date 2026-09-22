// The git vocabulary is shared by the Git panel, the viewer's marks chip
// and the file tree badges, so the pieces are pinned here: the letter,
// the tone class, the re-keying of a repo-relative snapshot onto the
// workspace-relative paths the tree speaks, and the folder roll-up that
// gives a directory the status of everything under it.
import { describe, expect, it } from 'vitest';
import en from '../i18n/locales/en.json';
import zh from '../i18n/locales/zh.json';
import {
  changeForPath,
  folderChangeMap,
  KIND_PRIORITY,
  KIND_LABEL,
  KIND_MARK,
  kindClass,
  workspaceChangeMap,
} from './gitKinds';
import type { GitChange, GitChangeKind, GitStatus } from './types';

const KINDS: GitChangeKind[] = [
  'added',
  'modified',
  'deleted',
  'renamed',
  'copied',
  'typechange',
  'untracked',
  'unmerged',
];

function change(over: Partial<GitChange> = {}): GitChange {
  return {
    path: 'a.go',
    kind: 'modified',
    staged: false,
    unstaged: true,
    untracked: false,
    unmerged: false,
    directory: false,
    is_binary: false,
    additions: 1,
    deletions: 0,
    in_workspace: true,
    ...over,
  };
}

function status(over: Partial<GitStatus> = {}): GitStatus {
  return {
    root: '/repo',
    workspace: '/repo',
    truncated: false,
    entries: [],
    ...over,
  };
}

/** lookup resolves a dotted key in a locale document. */
function lookup(doc: unknown, key: string): unknown {
  return key
    .split('.')
    .reduce<unknown>(
      (node, part) =>
        node && typeof node === 'object'
          ? (node as Record<string, unknown>)[part]
          : undefined,
      doc,
    );
}

describe('the kind vocabulary', () => {
  it('answers every kind with a letter and a tone', () => {
    for (const kind of KINDS) {
      expect(KIND_MARK[kind]).toBeTruthy();
      expect(kindClass(kind)).toMatch(/text-/);
    }
  });

  it('points every label at a key both locales define', () => {
    for (const kind of KINDS) {
      const key = KIND_LABEL[kind];
      expect(typeof lookup(en, key)).toBe('string');
      expect(typeof lookup(zh, key)).toBe('string');
    }
  });
});

describe('workspaceChangeMap', () => {
  it('keys a workspace that is the repository root by entry path', () => {
    const map = workspaceChangeMap(
      status({
        entries: [change({ path: 'src/a.go' }), change({ path: 'b.ts' })],
      }),
    );
    expect(Object.keys(map).sort()).toEqual(['b.ts', 'src/a.go']);
  });

  it('trims the workspace prefix when the repository is a parent', () => {
    const map = workspaceChangeMap(
      status({
        root: '/repo',
        workspace: '/repo/packages/app',
        entries: [
          change({ path: 'packages/app/src/a.go' }),
          change({ path: 'packages/other/b.go' }),
          change({ path: 'README.md', in_workspace: false }),
        ],
      }),
    );
    expect(Object.keys(map)).toEqual(['src/a.go']);
  });

  it('tolerates trailing separators and backslashes', () => {
    const map = workspaceChangeMap(
      status({
        root: 'C:\\repo\\',
        workspace: 'C:\\repo\\pkg',
        entries: [change({ path: 'pkg/a.go' })],
      }),
    );
    expect(Object.keys(map)).toEqual(['a.go']);
  });

  it('reports nothing for a missing snapshot', () => {
    expect(workspaceChangeMap(null)).toEqual({});
    expect(workspaceChangeMap(status({ workspace: undefined }))).toEqual({});
  });
});

describe('folderChangeMap', () => {
  it('rolls every path up onto its directories and the root', () => {
    const map = workspaceChangeMap(
      status({
        entries: [
          change({ path: 'internal/files/a.go' }),
          change({ path: 'internal/repo/b.go' }),
          change({ path: 'README.md' }),
        ],
      }),
    );
    const folders = folderChangeMap(map);
    expect(Object.keys(folders).sort()).toEqual(
      ['.', 'internal', 'internal/files', 'internal/repo'].sort(),
    );
    expect(folders['.'].files).toBe(3);
    expect(folders['internal'].files).toBe(2);
    expect(folders['internal/files'].files).toBe(1);
    // A folder that holds nothing changed has no entry at all.
    expect(folders['internal/other']).toBeUndefined();
  });

  it('counts a collapsed untracked directory as its own folder', () => {
    // git reports `dir/` alone, so the folder itself carries the mark
    // its (unlisted) children would have had.
    const map = workspaceChangeMap(
      status({
        entries: [
          change({
            path: 'docs/new',
            kind: 'untracked',
            staged: false,
            unstaged: false,
            untracked: true,
            directory: true,
          }),
        ],
      }),
    );
    const folders = folderChangeMap(map);
    expect(folders['docs/new'].files).toBe(1);
    expect(folders['docs/new'].kind).toBe('untracked');
    expect(folders['docs'].kind).toBe('untracked');
    expect(folders['.'].files).toBe(1);
  });

  it('keeps the loudest kind and flags the mixture', () => {
    const map = workspaceChangeMap(
      status({
        entries: [
          change({ path: 'src/a.go', kind: 'untracked', untracked: true }),
          change({ path: 'src/b.go', kind: 'modified' }),
          change({ path: 'src/c.go', kind: 'modified' }),
        ],
      }),
    );
    const folder = folderChangeMap(map)['src'];
    expect(folder.kind).toBe('modified');
    expect(folder.mixed).toBe(true);
    expect(folder.kinds).toEqual({ modified: 2, untracked: 1 });

    // One kind alone is not a mixture.
    const single = folderChangeMap(
      workspaceChangeMap(status({ entries: [change({ path: 'src/b.go' })] })),
    )['src'];
    expect(single.kind).toBe('modified');
    expect(single.mixed).toBe(false);
  });

  it('prefers attention-grabbing kinds over the quiet ones', () => {
    const kinds = new Set<GitChangeKind>();
    for (const kind of KIND_PRIORITY) {
      const map = workspaceChangeMap(
        status({
          entries: [
            change({ path: `src/${kind}.go`, kind }),
            change({ path: 'src/loud.go', kind: 'unmerged', unmerged: true }),
          ],
        }),
      );
      kinds.add(folderChangeMap(map)['src'].kind);
    }
    // A conflict inside a folder outranks everything else in it, and the
    // priority list itself covers every kind exactly once.
    expect([...kinds]).toEqual(['unmerged']);
    expect(new Set(KIND_PRIORITY).size).toBe(KINDS.length);
  });

  it('sums the line counts but not the binary or unnumbered ones', () => {
    const map = workspaceChangeMap(
      status({
        entries: [
          change({ path: 'src/a.go', additions: 4, deletions: 2 }),
          change({
            path: 'src/logo.png',
            is_binary: true,
            additions: 0,
            deletions: 0,
          }),
          change({
            path: 'src/new',
            kind: 'untracked',
            untracked: true,
            directory: true,
            additions: 0,
            deletions: 0,
          }),
        ],
      }),
    );
    const folder = folderChangeMap(map)['src'];
    expect(folder.additions).toBe(4);
    expect(folder.deletions).toBe(2);
    expect(folder.binary).toBe(true);
    expect(folder.files).toBe(3);
  });

  it('reports the staged and working-tree split of its paths', () => {
    const map = workspaceChangeMap(
      status({
        entries: [
          change({ path: 'src/a.go', staged: true, unstaged: false }),
          change({ path: 'src/b.go', unstaged: true }),
        ],
      }),
    );
    const folder = folderChangeMap(map)['src'];
    expect(folder.staged).toBe(true);
    expect(folder.unstaged).toBe(true);
  });

  it('carries a truncated snapshot into every folder', () => {
    const map = workspaceChangeMap(
      status({ entries: [change({ path: 'a.go' })] }),
    );
    expect(folderChangeMap(map)['.'].truncated).toBe(false);
    expect(folderChangeMap(map, true)['.'].truncated).toBe(true);
    expect(folderChangeMap({}, false)).toEqual({});
  });
});

describe('changeForPath', () => {
  it('answers a file with its own entry', () => {
    const map = workspaceChangeMap(
      status({ entries: [change({ path: 'src/a.go' })] }),
    );
    expect(changeForPath(map, 'src/a.go')?.kind).toBe('modified');
  });

  it('inherits the mark of a collapsed untracked ancestor', () => {
    // The file inside `docs/new/` has no entry of its own; git only
    // reported the directory. The row still reads as untracked.
    const map = workspaceChangeMap(
      status({
        entries: [
          change({
            path: 'docs/new',
            kind: 'untracked',
            staged: false,
            unstaged: false,
            untracked: true,
            directory: true,
          }),
        ],
      }),
    );
    const inherited = changeForPath(map, 'docs/new/deep/note.md');
    expect(inherited?.kind).toBe('untracked');
    expect(inherited?.directory).toBe(false);
    expect(inherited?.path).toBe('docs/new/deep/note.md');
    expect(inherited?.additions).toBe(0);
    // A clean file under a known directory stays clean.
    expect(changeForPath(map, 'docs/readme.md')).toBeUndefined();
  });
});
