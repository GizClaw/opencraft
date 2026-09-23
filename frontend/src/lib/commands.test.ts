import { describe, expect, it, vi } from 'vitest';
import { buildCommands, filterCommands, type Command } from './commands';

function command(over: Partial<Command> & { title: string }): Command {
  return {
    id: over.title,
    group: 'palette.groupActions',
    icon: () => null,
    run: () => {},
    ...over,
  };
}

function actions() {
  return {
    newChat: vi.fn(),
    openConfig: vi.fn(),
    openTools: vi.fn(),
    openFiles: vi.fn(),
    chooseWorkspace: vi.fn(),
    openWorkspace: vi.fn(),
    openSessionInWorkspace: vi.fn(),
    setTheme: vi.fn(),
    runShortcut: vi.fn(),
  };
}

describe('filterCommands', () => {
  const commands = [
    command({ title: 'New chat', keywords: '新建 对话' }),
    command({ title: 'Usage', keywords: '用量 tokens' }),
    command({ title: 'Process PATH', keywords: '路径' }),
    command({ title: 'Browse files', keywords: '文件' }),
  ];

  it('returns the catalogue unchanged for an empty query', () => {
    expect(filterCommands(commands, '   ')).toEqual(commands);
  });

  it('ranks a prefix above a later word and a keyword hit', () => {
    const ranked = filterCommands(commands, 'us');
    expect(ranked[0].title).toBe('Usage');
  });

  it('finds a command by a word inside its title', () => {
    expect(filterCommands(commands, 'path')[0].title).toBe('Process PATH');
  });

  it('still matches through keywords in the other language', () => {
    expect(filterCommands(commands, '文件')[0].title).toBe('Browse files');
    expect(filterCommands(commands, '用量')[0].title).toBe('Usage');
  });

  it('accepts a subsequence for a half-remembered name', () => {
    expect(filterCommands(commands, 'ncht')[0].title).toBe('New chat');
  });

  it('drops commands that do not match at all', () => {
    expect(filterCommands(commands, 'zzzz')).toEqual([]);
  });
});

describe('buildCommands', () => {
  const t = (key: string) => key;

  it('offers every settings tab, every tools page and the live lists', () => {
    const commands = buildCommands({
      t,
      actions: actions(),
      state: {
        theme: 'dark',
        workspace: '/tmp/a',
        workspaces: [
          { id: 'w-a', path: '/tmp/a', title: 'Alpha', last_opened: '' },
          { id: 'w-b', path: '/tmp/b', title: 'Beta', last_opened: '' },
        ],
        sessions: [{ id: 's-1', title: 'Fix the parser' }] as never,
      },
    });
    const ids = commands.map((c) => c.id);
    expect(ids).toContain('settings:diagnostics');
    expect(ids).toContain('tools:plugins');
    // The active workspace is not offered as a switch target.
    expect(ids).toContain('workspace:w-b');
    expect(ids).not.toContain('workspace:w-a');
    expect(ids).toContain('session:s-1');
    // The theme the user is already on is not a command.
    expect(ids).not.toContain('theme:dark');
    expect(ids).toContain('theme:light');
  });

  it('runs the action of the command it built', () => {
    const spy = actions();
    const commands = buildCommands({
      t,
      actions: spy,
      state: { theme: 'dark', workspace: '', workspaces: [], sessions: [] },
    });
    commands.find((c) => c.id === 'settings:usage')?.run();
    commands.find((c) => c.id === 'nav:files')?.run();
    expect(spy.openConfig).toHaveBeenCalledWith('usage');
    expect(spy.openFiles).toHaveBeenCalled();
  });
});
