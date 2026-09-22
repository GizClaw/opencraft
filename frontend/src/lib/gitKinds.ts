// The git change vocabulary shared by the Git panel and the file
// viewer's marks: the one-letter badge, its tone class, and the panel's
// staged/unstaged/untracked/conflict grouping.
import type { GitChange, GitChangeKind, GitStatus } from './types';

export const KIND_MARK: Record<GitChangeKind, string> = {
  added: 'A',
  modified: 'M',
  deleted: 'D',
  renamed: 'R',
  copied: 'C',
  typechange: 'T',
  untracked: '?',
  unmerged: 'U',
};

/**
 * KIND_LABEL is the translation key of the spelled-out kind, for the
 * places that show a sentence instead of a letter (the viewer's marks
 * tooltip). Untracked and unmerged reuse the group labels: they read
 * the same as a kind.
 */
export const KIND_LABEL: Record<GitChangeKind, string> = {
  added: 'git.kindAdded',
  modified: 'git.kindModified',
  deleted: 'git.kindDeleted',
  renamed: 'git.kindRenamed',
  copied: 'git.kindCopied',
  typechange: 'git.kindTypechange',
  untracked: 'git.untracked',
  unmerged: 'git.unmerged',
};

export function kindClass(kind: GitChangeKind): string {
  switch (kind) {
    case 'added':
      return 'bg-ok/15 text-ok';
    case 'deleted':
      return 'bg-err/15 text-err';
    case 'unmerged':
      return 'bg-warn/15 text-warn';
    case 'untracked':
      return 'bg-dim/10 text-dim';
    default:
      return 'bg-accent/15 text-accent';
  }
}

export function groupEntries(entries: GitChange[]): {
  staged: GitChange[];
  unstaged: GitChange[];
  untracked: GitChange[];
  unmerged: GitChange[];
} {
  const groups = { staged: [], unstaged: [], untracked: [], unmerged: [] } as {
    staged: GitChange[];
    unstaged: GitChange[];
    untracked: GitChange[];
    unmerged: GitChange[];
  };
  for (const e of entries) {
    if (e.unmerged) groups.unmerged.push(e);
    else if (e.untracked) groups.untracked.push(e);
    else if (e.staged) groups.staged.push(e);
    else groups.unstaged.push(e);
  }
  return groups;
}

/**
 * workspaceChangeMap re-keys a status snapshot by workspace-relative
 * path, which is the coordinate the file tree and quick-open speak.
 * Entries are repo-relative, so a workspace that is a repository
 * subtree needs its own prefix trimmed; entries outside the workspace
 * (the panel is a whole-repository tool) have no tree row to sit on and
 * are dropped.
 */
export function workspaceChangeMap(
  status: GitStatus | null | undefined,
): Record<string, GitChange> {
  const out: Record<string, GitChange> = {};
  if (!status) return out;
  const root = (status.root ?? '').replace(/\\/g, '/').replace(/\/+$/, '');
  const ws = (status.workspace ?? '').replace(/\\/g, '/').replace(/\/+$/, '');
  const prefix =
    root && ws.startsWith(root)
      ? ws.slice(root.length).replace(/^\/+/, '')
      : '';
  for (const entry of status.entries ?? []) {
    if (!entry.in_workspace) continue;
    let rel = entry.path;
    if (prefix) {
      if (!rel.startsWith(`${prefix}/`)) continue;
      rel = rel.slice(prefix.length + 1);
    }
    out[rel] = entry;
  }
  return out;
}

/**
 * KIND_PRIORITY orders the kinds by how loudly one letter should speak
 * for a folder holding several of them: a conflict blocks work, a
 * deletion means a file is gone, an edit changed what is there, and
 * "merely new" is the quietest thing a folder can say.
 */
export const KIND_PRIORITY: GitChangeKind[] = [
  'unmerged',
  'deleted',
  'modified',
  'typechange',
  'renamed',
  'copied',
  'added',
  'untracked',
];

/** FolderChange is the aggregate of every changed path under a folder. */
export interface FolderChange {
  /**
   * files counts changed paths the way git reported them: a collapsed
   * untracked directory is one path, not the files inside it.
   */
  files: number;
  additions: number;
  deletions: number;
  /** kinds counts each kind present, for the tooltip's breakdown. */
  kinds: Partial<Record<GitChangeKind, number>>;
  /** kind is the headline kind, picked by KIND_PRIORITY. */
  kind: GitChangeKind;
  /** mixed is true when the folder's paths disagree on the kind. */
  mixed: boolean;
  staged: boolean;
  unstaged: boolean;
  untracked: boolean;
  unmerged: boolean;
  binary: boolean;
  /** truncated notes that the status snapshot itself was cut short. */
  truncated: boolean;
}

/**
 * folderChangeMap rolls the change map up onto its directories, the way
 * an explorer marks a folder that holds anything changed: one headline
 * letter, with the breakdown left to the tooltip. Keys are
 * workspace-relative directory paths plus '.' for the root, the same
 * vocabulary the file tree navigates by.
 *
 * The roll-up is complete rather than "what the tree happens to have
 * listed": the snapshot covers the whole repository, so a collapsed
 * folder is already marked before it is ever expanded.
 */
export function folderChangeMap(
  changes: Record<string, GitChange>,
  truncated = false,
): Record<string, FolderChange> {
  const folders: Record<string, FolderChange> = {};
  const bump = (dir: string, entry: GitChange) => {
    const acc = (folders[dir] ??= {
      files: 0,
      additions: 0,
      deletions: 0,
      kinds: {},
      kind: 'modified',
      mixed: false,
      staged: false,
      unstaged: false,
      untracked: false,
      unmerged: false,
      binary: false,
      truncated,
    });
    acc.files += 1;
    // A directory entry stands for content git never numbered; the
    // same goes for a binary change.
    if (!entry.is_binary && !entry.directory) {
      acc.additions += entry.additions;
      acc.deletions += entry.deletions;
    }
    acc.kinds[entry.kind] = (acc.kinds[entry.kind] ?? 0) + 1;
    acc.staged ||= entry.staged;
    acc.unstaged ||= entry.unstaged;
    acc.untracked ||= entry.untracked;
    acc.unmerged ||= entry.unmerged;
    acc.binary ||= entry.is_binary;
  };
  for (const entry of Object.values(changes)) {
    // A collapsed untracked directory is a change of its own: git
    // reports `dir/` and nothing inside it, so the folder carries the
    // mark its children would have had.
    if (entry.directory) bump(entry.path, entry);
    for (const dir of ancestorsOf(entry.path)) bump(dir, entry);
  }
  for (const acc of Object.values(folders)) {
    const present = KIND_PRIORITY.filter((kind) => acc.kinds[kind]);
    acc.kind = present[0] ?? 'modified';
    acc.mixed = present.length > 1;
  }
  return folders;
}

/**
 * changeForPath resolves the change that applies to one file row. git
 * lists a collapsed untracked directory as a single entry, so a file
 * inside one has no entry of its own and inherits the mark from that
 * ancestor — the same answer the folder badge above it gives.
 */
export function changeForPath(
  changes: Record<string, GitChange>,
  path: string,
): GitChange | undefined {
  const own = changes[path];
  if (own) return own;
  let cut = path.lastIndexOf('/');
  while (cut > 0) {
    const dir = changes[path.slice(0, cut)];
    if (dir?.directory && dir.untracked) {
      return { ...dir, path, directory: false, additions: 0, deletions: 0 };
    }
    cut = path.lastIndexOf('/', cut - 1);
  }
  return undefined;
}

/** ancestorsOf lists the folder keys a path sits in, root included. */
function ancestorsOf(path: string): string[] {
  const parts = path.split('/');
  const out = ['.'];
  for (let i = 0; i < parts.length - 1; i += 1) {
    out.push(parts.slice(0, i + 1).join('/'));
  }
  return out;
}
