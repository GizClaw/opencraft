// Workspace paths cross one boundary where their spelling matters: the
// backend announces the active workspace in the ready event and the
// window switches on the string it carries against the history row the
// user clicked. Both sides are written in the one spelling config
// defines (`config.CanonicalWorkDir`), so they normally agree character
// for character; a path that reached the window another way — a picker
// result, a dialog's answer — may still carry a trailing separator, and
// comparing it verbatim would make one directory look like two.
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

/**
 * workspaceLabel is the folder a workspace path names — what the UI shows
 * as its title. Diagnostics use it too: a message that carries the full
 * path reaches the telemetry log as `<path>` (frontendErrors redacts
 * absolute paths), which would say nothing about which workspace was
 * involved.
 */
export function workspaceLabel(path: string): string {
  const cut = path.trim().replace(/[/\\]+$/, '');
  const at = Math.max(cut.lastIndexOf('/'), cut.lastIndexOf('\\'));
  return at < 0 ? cut : cut.slice(at + 1);
}
