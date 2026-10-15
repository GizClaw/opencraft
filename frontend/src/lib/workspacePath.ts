// Workspace paths cross one boundary where their spelling matters: the
// backend announces the active workspace in the ready event and the
// window switches on the string it carries, while workspace history
// rows carry `filepath.Clean`'d paths. The backend canonicalizes what
// it announces (see core.SetWorkDir), so the two normally agree
// character for character; a path that reached the window another way —
// a launch flag the user typed, a binding result — may still carry a
// trailing separator, and comparing it verbatim would make one
// directory look like two.
//
// Only separators are folded here. Case and symlinks are deliberately
// left alone: on a case-insensitive filesystem the two spellings do
// name one directory, but the window cannot tell whether it is on one,
// and guessing would be guessing about the filesystem rather than about
// the path.
export function sameWorkspacePath(a: string, b: string): boolean {
  const norm = (path: string): string => {
    const trimmed = path.trim();
    if (trimmed === '') return '';
    const cut = trimmed.replace(/[/\\]+$/, '');
    // A root ("/", "C:\") is all separator and stays itself.
    return cut === '' ? (trimmed.startsWith('\\') ? '\\' : '/') : cut;
  };
  const left = norm(a);
  return left !== '' && left === norm(b);
}
