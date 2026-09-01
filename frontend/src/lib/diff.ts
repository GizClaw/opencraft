// Unified-diff rendering helpers: the chat shows files that turn out to
// be git diffs (e.g. read_file on a .diff artifact) as GitDiffView
// cards instead of raw JSON or plain text. Parsing happens client-side
// because the file may be a historical snapshot whose content no longer
// matches the workspace (server-side RenderPatch computes against
// current files and would fail).

import type { PatchFileDTO, PatchLineDTO } from './types';

// looksLikeUnifiedDiff reports whether text is a git-style unified
// diff: either explicit "diff --git" headers or the "---/+++" file-pair
// convention followed by a hunk header.
export function looksLikeUnifiedDiff(text: string): boolean {
  if (/^diff --git /m.test(text)) return true;
  const oldIdx = text.indexOf('--- ');
  if (oldIdx < 0) return false;
  const newIdx = text.indexOf('+++ ', oldIdx + 4);
  return (
    newIdx > oldIdx &&
    /^@@ -\d+(,\d+)? \+\d+(,\d+)? @@/m.test(text.slice(newIdx))
  );
}

// parseUnifiedDiff converts a unified diff document into the rendered
// PatchFileDTO list consumed by GitDiffView. Returns null when the text
// is not a parseable diff.
export function parseUnifiedDiff(text: string): PatchFileDTO[] | null {
  if (!looksLikeUnifiedDiff(text)) return null;

  const lines = text.split(/\r?\n/);
  const files: PatchFileDTO[] = [];
  let cur: PatchFileDTO | null = null;
  let oldNum = 0;
  let newNum = 0;
  // pendingOld is the "---" path of the file section currently being
  // read; it pairs with the next "+++" path to open the file record.
  let pendingOld: string | null = null;
  let inHunk = false;
  // Headers ("---", "+++", "@@") are only recognised at section
  // boundaries — after "diff --git", a blank line, or file start — so a
  // removed line whose content happens to start with "--" is not
  // misread as a header.
  let atBoundary = true;

  const flush = () => {
    cur = null;
    oldNum = 0;
    newNum = 0;
    pendingOld = null;
    inHunk = false;
    atBoundary = true;
  };

  const pushLine = (kind: PatchLineDTO['kind'], text: string) => {
    if (!cur) return;
    const line: PatchLineDTO = { kind, text };
    if (kind === 'delete') {
      oldNum++;
      line.old_num = oldNum;
    } else if (kind === 'add') {
      newNum++;
      line.new_num = newNum;
    } else {
      oldNum++;
      newNum++;
      line.old_num = oldNum;
      line.new_num = newNum;
    }
    cur.lines.push(line);
    if (kind === 'add') cur.added++;
    if (kind === 'delete') cur.removed++;
  };

  const startFile = (oldPath: string, newPath: string) => {
    const oldDev = oldPath === '/dev/null' || oldPath.startsWith('dev/null');
    const newDev = newPath === '/dev/null' || newPath.startsWith('dev/null');
    const action: PatchFileDTO['action'] = oldDev
      ? 'add'
      : newDev
        ? 'delete'
        : 'update';
    cur = {
      path: newDev ? oldPath : newPath,
      action,
      added: 0,
      removed: 0,
      lines: [],
    };
    oldNum = 0;
    newNum = 0;
    inHunk = true;
    files.push(cur);
  };

  const unquote = (p: string) =>
    p.length >= 2 && p.startsWith('"') && p.endsWith('"')
      ? p.slice(1, -1).replace(/\\"/g, '"')
      : p;

  for (const raw of lines) {
    const line = raw.endsWith('\r') ? raw.slice(0, -1) : raw;

    // "diff --git a/<old> b/<new>" starts a new file section.
    if (/^diff --git /.test(line)) {
      flush();
      continue;
    }

    // "--- a/<path>" (or "--- /dev/null") announces the old side.
    const om = atBoundary && /^--- (?:"?(?:a\/)?(.+?)"?)$/.exec(line);
    if (om) {
      pendingOld = unquote(om[1]);
      continue;
    }

    // "+++ b/<path>" announces the new side and opens the file record.
    const nm = atBoundary && /^\+\+\+ (?:"?(?:b\/)?(.+?)"?)$/.exec(line);
    if (nm) {
      const oldPath = pendingOld ?? (nm[1] === '/dev/null' ? '/dev/null' : '');
      startFile(oldPath, unquote(nm[1]));
      pendingOld = null;
      continue;
    }

    // Hunk headers are recognised anywhere inside a file section: a
    // content line can never start with "@@" (context lines carry a
    // leading space, removals a leading "-"), and git emits hunks back
    // to back without blank separators.
    const hm = /^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@/.exec(line);
    if (hm) {
      // Hunk header: seed the counters so a leading context line (or
      // the first content line) carries correct numbers.
      oldNum = parseInt(hm[1], 10) - 1;
      newNum = parseInt(hm[3], 10) - 1;
      inHunk = true;
      atBoundary = false;
      continue;
    }

    if (!inHunk || !cur) continue;
    atBoundary = false;
    if (line.startsWith('\\')) continue; // "\ No newline at end of file"
    if (line.startsWith(' ')) {
      pushLine('context', line.slice(1));
    } else if (line.startsWith('-')) {
      pushLine('delete', line.slice(1));
    } else if (line.startsWith('+')) {
      pushLine('add', line.slice(1));
    } else if (line === '') {
      // Blank separator between hunks: allow the next hunk header.
      atBoundary = true;
      continue;
    }
  }

  return files.length > 0 ? files : null;
}

// recoverJsonContent extracts a usable string from a tool result that
// fails strict JSON parsing. Old archives occasionally contain a raw
// newline inside the read_file envelope's JSON string (the envelope
// then no longer parses), which previously fell through to the raw red
// result display. The envelope shape is fixed:
//
//	{"content":"<text>","file_path":...,"is_truncated":...}
//
// so the content can be recovered leniently: scan the string, honour
// backslash escapes, and stop at the closing quote before the next key.
export function recoverJsonContent(raw: string): string | null {
  const marker = '{"content":"';
  const i = raw.indexOf(marker);
  if (i < 0) return null;
  let out = '';
  let j = i + marker.length;
  while (j < raw.length) {
    const ch = raw[j];
    if (ch === '\\' && j + 1 < raw.length) {
      const next = raw[j + 1];
      if (next === 'n') out += '\n';
      else if (next === 't') out += '\t';
      else if (next === 'r') out += '\r';
      else if (next === '"') out += '"';
      else if (next === '\\') out += '\\';
      else if (next === 'u') {
        const hex = raw.slice(j + 2, j + 6);
        if (/^[0-9a-fA-F]{4}$/.test(hex)) {
          out += String.fromCharCode(parseInt(hex, 16));
          j += 4;
        } else {
          out += 'u';
        }
      } else {
        out += next;
      }
      j += 2;
      continue;
    }
    if (ch === '"') {
      // Closing quote of the content string (followed by a key or
      // end-of-envelope punctuation).
      const rest = raw.slice(j + 1).trimStart();
      if (rest === '' || rest.startsWith(',')) return out;
      // A literal quote inside unescaped content (corrupt input); keep
      // scanning past it.
      out += ch;
      j++;
      continue;
    }
    out += ch;
    j++;
  }
  return out;
}
