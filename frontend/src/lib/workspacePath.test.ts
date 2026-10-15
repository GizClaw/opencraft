import { describe, expect, it } from 'vitest';
import { sameWorkspacePath } from './workspacePath';

describe('sameWorkspacePath', () => {
  it('treats a trailing separator as the same workspace', () => {
    expect(sameWorkspacePath('/Users/x/proj/', '/Users/x/proj')).toBe(true);
    expect(sameWorkspacePath('/Users/x/proj', '/Users/x/proj//')).toBe(true);
    expect(sameWorkspacePath('C:\\work\\proj\\', 'C:\\work\\proj')).toBe(true);
  });

  it('keeps distinct directories apart', () => {
    expect(sameWorkspacePath('/Users/x/proj', '/Users/x/other')).toBe(false);
    expect(sameWorkspacePath('/Users/x/proj', '/Users/x/proj/nested')).toBe(
      false,
    );
    // Case is not folded: the window cannot know whether the filesystem
    // would.
    expect(sameWorkspacePath('/Users/x/Proj', '/Users/x/proj')).toBe(false);
  });

  it('treats an empty side as no workspace', () => {
    expect(sameWorkspacePath('', '')).toBe(false);
    expect(sameWorkspacePath('   ', '/Users/x/proj')).toBe(false);
    expect(sameWorkspacePath('/Users/x/proj', '')).toBe(false);
  });

  it('keeps a filesystem root a root', () => {
    expect(sameWorkspacePath('/', '/')).toBe(true);
    expect(sameWorkspacePath('/', '/Users')).toBe(false);
  });
});
