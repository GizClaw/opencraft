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
