import { api } from './api';
import type { ResolvedTarget } from './types';

// LinkTarget is one clicked document link after classification. Links
// appear in chat markdown, SKILL.md bodies and file previews, and every
// surface has to dispose of them the same way: URL schemes go to the
// system browser; everything else — file:// URLs and Windows drive
// paths included — resolves as a local path so the caller can render
// it.
export type LinkTarget =
  | { kind: 'ignored' }
  | { kind: 'external'; url: string }
  | { kind: 'dir'; target: ResolvedTarget }
  | { kind: 'file'; target: ResolvedTarget };

// Windows drive paths (C:\dir\file.md) look like URL schemes but are
// plain local targets, so they take the resolve path like any other.
// A file:// URL names one local file, so it does too.
const WINDOWS_DRIVE = /^[A-Za-z]:[\\/]/;
const FILE_URL = /^file:/i;
const URL_SCHEME = /^[a-zA-Z][a-zA-Z0-9+.-]*:/;

export function isExternalHref(href: string): boolean {
  return (
    !WINDOWS_DRIVE.test(href) && !FILE_URL.test(href) && URL_SCHEME.test(href)
  );
}

const SEPARATOR = /[/\\]/;
// MAX_CODE_LINK_CHARS keeps a pasted wall of text in one code span from
// being treated as a path.
const MAX_CODE_LINK_CHARS = 240;
// LINE_SUFFIX matches the `:line[:column]` tag agents write after a path
// (`internal/orchestration/host/pool.go:212`). It is peeled only when
// what remains still looks like a path, so a time (`12:30`) or a
// version tag is left alone.
const LINE_SUFFIX = /^(.*?):\d{1,6}(?::\d{1,6})?$/;

// codeLinkTarget decides whether one inline code span names something
// the reader can open, and returns what to resolve. Assistants write
// file paths and URLs in backticks as freely as they write identifiers
// and commands, so the bar is deliberately narrow: one token, spelled
// like a path or a URL, with no punctuation a snippet would carry.
// Everything else stays plain text (`npm test`, `Object.keys`,
// `graph.name`).
export function codeLinkTarget(text: string): string | null {
  const raw = text.trim();
  if (!raw || raw.length > MAX_CODE_LINK_CHARS) return null;
  // A phrase, a quoted string, a redirected snippet or a comparison is
  // never one target.
  if (/[\s`"'<>|]/.test(raw)) return null;
  if (/[{}[\]()]/.test(raw)) return null;
  // A path is tried first, tag and all: `store.ts:42` reads as the
  // scheme `store.ts` to any general URL test, and only the path branch
  // can tell it from a URL.
  const head = splitLineSuffix(raw);
  for (const candidate of [head, raw]) {
    if (/[?;=]/.test(candidate)) continue;
    if (isPathLikeToken(candidate)) return candidate;
  }
  // A URL may carry a query string, and only schemes that name a real
  // service count: a code span has no `//` to separate a URL from a
  // token with a colon in it.
  if (CODE_URL.test(raw)) return raw;
  return null;
}

const CODE_URL = /^(?:https?|ftps?|ssh|git|mailto|tel|file|vscode|craft):/i;

// splitLineSuffix peels the `:line[:column]` tag off a path so the click
// resolves the file the tag points into. A tag whose head is not a path
// is left in place.
export function splitLineSuffix(token: string): string {
  const match = LINE_SUFFIX.exec(token);
  if (!match || match[1] === '') return token;
  return isPathLikeToken(match[1]) ? match[1] : token;
}

// SEGMENT is one path segment: the characters a file or directory name
// uses, without separators.
const SEGMENT = /^[\w.@+-]+$/;

// isPathLikeToken answers whether one token is spelled like a local
// path. An explicit path — absolute, home, or dot-relative — counts
// whatever it names, a directory included; anything else has to end in
// a file name the reader recognizes, so `and/or` stays prose while
// `docs/architecture.md` and `main.go` do not.
export function isPathLikeToken(token: string): boolean {
  if (!token) return false;
  if (SEPARATOR.test(token)) {
    if (
      /^([/\\]|\.{1,2}[/\\]|~[/\\])/.test(token) ||
      WINDOWS_DRIVE.test(token)
    ) {
      return true;
    }
    const parts = token.split(/[/\\]/);
    const last = parts[parts.length - 1] ?? '';
    return parts.every((part) => SEGMENT.test(part)) && isKnownFileName(last);
  }
  return isKnownFileName(token);
}

// FILE_EXTENSIONS is the vocabulary that tells a file name written in
// backticks from an identifier that merely contains a dot: `craft.yaml`
// and `main.go` are files, `state.ready` and `graph.name` are not.
const FILE_EXTENSIONS = new Set([
  'md',
  'markdown',
  'mdx',
  'txt',
  'rst',
  'adoc',
  'go',
  'ts',
  'tsx',
  'js',
  'jsx',
  'mjs',
  'cjs',
  'mts',
  'cts',
  'vue',
  'svelte',
  'py',
  'rb',
  'rs',
  'java',
  'kt',
  'kts',
  'c',
  'h',
  'cc',
  'cpp',
  'cxx',
  'hpp',
  'cs',
  'swift',
  'm',
  'mm',
  'php',
  'lua',
  'pl',
  'r',
  'scala',
  'dart',
  'ex',
  'exs',
  'erl',
  'hs',
  'clj',
  'sh',
  'bash',
  'zsh',
  'fish',
  'ps1',
  'bat',
  'cmd',
  'json',
  'jsonc',
  'json5',
  'yaml',
  'yml',
  'toml',
  'ini',
  'cfg',
  'conf',
  'env',
  'properties',
  'lock',
  'mod',
  'sum',
  'plist',
  'xml',
  'html',
  'htm',
  'css',
  'scss',
  'sass',
  'less',
  'sql',
  'proto',
  'graphql',
  'gql',
  'http',
  'diff',
  'patch',
  'csv',
  'tsv',
  'log',
  'db',
  'sqlite',
  'sqlite3',
  'png',
  'jpg',
  'jpeg',
  'gif',
  'webp',
  'svg',
  'ico',
  'bmp',
  'tiff',
  'avif',
  'pdf',
  'zip',
  'tar',
  'gz',
  'tgz',
  'bz2',
  'xz',
  '7z',
  'rar',
  'mp3',
  'wav',
  'flac',
  'ogg',
  'm4a',
  'mp4',
  'mov',
  'm4v',
  'webm',
  'mkv',
  'avi',
]);

// FILE_NAMES are the extensionless files common enough to recognize.
const FILE_NAMES = new Set([
  'Makefile',
  'Dockerfile',
  'LICENSE',
  'Procfile',
  '.env',
  '.gitignore',
  '.gitattributes',
]);

function isKnownFileName(name: string): boolean {
  if (!name || name.endsWith('.')) return false;
  if (FILE_NAMES.has(name)) return true;
  const dot = name.lastIndexOf('.');
  if (dot <= 0) return false;
  return FILE_EXTENSIONS.has(name.slice(dot + 1).toLowerCase());
}

// resolveLinkTarget classifies one click without side effects, so each
// caller decides where a directory or a file ends up (chat rail tab,
// dialog page, system file manager). Empty targets and `#anchors` are
// ignored: they point inside the document that is already on screen.
export async function resolveLinkTarget(
  href: string,
  base = '',
): Promise<LinkTarget> {
  const raw = href.trim();
  if (!raw || raw.startsWith('#')) return { kind: 'ignored' };
  if (isExternalHref(raw)) return { kind: 'external', url: raw };
  const target = await api.resolveTarget(raw, base);
  return target.is_dir ? { kind: 'dir', target } : { kind: 'file', target };
}

// followLinkTarget performs one clicked link's side effects: URL
// schemes reach the system browser, directories the system file manager
// (or the caller's own handler, e.g. the chat's file tree) and files
// the caller's renderer — a viewer tab in the chat, a dialog page
// anywhere else. A failure lands in onError as the backend's message,
// so a click never dies silently.
export async function followLinkTarget(
  href: string,
  base: string,
  handlers: {
    openFile: (target: ResolvedTarget) => void;
    onError: (message: string) => void;
    openDir?: (target: ResolvedTarget) => void | Promise<void>;
  },
): Promise<void> {
  const { openFile, onError, openDir } = handlers;
  let link: LinkTarget;
  try {
    link = await resolveLinkTarget(href, base);
  } catch (err) {
    onError(String(err));
    return;
  }
  if (link.kind === 'ignored') return;
  try {
    if (link.kind === 'external') {
      await api.openExternal(link.url);
      return;
    }
    if (link.kind === 'dir') {
      await (openDir
        ? openDir(link.target)
        : api.revealArtifact(link.target.path));
      return;
    }
  } catch (err) {
    onError(String(err));
    return;
  }
  openFile(link.target);
}
