import type { ComponentType } from 'react';
import {
  BarChart3,
  Bot,
  CalendarClock,
  Copy,
  Cpu,
  Database,
  FileText,
  FolderOpen,
  Import,
  Keyboard,
  MessageSquare,
  Moon,
  Package,
  Palette,
  ShieldCheck,
  SlidersHorizontal,
  Sparkles,
  SquarePen,
  Stethoscope,
  TextCursorInput,
  Wrench,
} from 'lucide-react';
import type { SessionMeta, WorkspaceMeta } from './types';
import type { ToolPage } from '../components/ToolsPanel';

// The command palette's catalogue. It is built from live app state on every
// open, so a command cannot point at a workspace, session or settings tab
// that no longer exists — the failure mode of a hand-maintained menu.
//
// Ranking is a pure function (`filterCommands`) so the behaviour that makes
// the palette usable — prefix beats substring beats a keyword hit — is
// testable without a DOM.
export interface Command {
  id: string;
  /** i18n key of the group heading, rendered when it changes. */
  group: string;
  title: string;
  /** Right-aligned second line: the path, the tab, the hint. */
  hint?: string;
  /** Extra search terms (both languages) that are not shown. */
  keywords?: string;
  /**
   * Shortcut id from lib/keys.ts. The palette renders the combo beside
   * the title and the dispatcher owns the key itself, so the badge and
   * the binding cannot disagree.
   */
  shortcut?: string;
  icon: ComponentType<{ size?: string | number; className?: string }>;
  run: () => void;
}

export interface CommandActions {
  newChat: () => void;
  openConfig: (tab?: string) => void;
  openTools: (view: ToolPage) => void;
  openFiles: () => void;
  chooseWorkspace: () => void;
  openWorkspace: (path: string) => void;
  openSessionInWorkspace: (sessionID: string, workspacePath: string) => void;
  setTheme: (theme: 'dark' | 'light' | 'auto') => void;
  /** Runs a command by its shortcut id — the same entry point the
   * keyboard listener and the native menu use. */
  runShortcut: (id: string) => void;
}

export interface CommandState {
  theme: 'dark' | 'light' | 'auto';
  workspace: string;
  workspaces: WorkspaceMeta[];
  sessions: SessionMeta[];
}

export interface CommandSources {
  t: (key: string, options?: Record<string, unknown>) => string;
  actions: CommandActions;
  state: CommandState;
}

const SETTINGS_TABS: { id: string; icon: Command['icon'] }[] = [
  { id: 'general', icon: SlidersHorizontal },
  { id: 'display', icon: Palette },
  { id: 'inference', icon: Cpu },
  { id: 'tools', icon: Wrench },
  { id: 'memory', icon: Database },
  { id: 'permissions', icon: ShieldCheck },
  { id: 'usage', icon: BarChart3 },
  { id: 'diagnostics', icon: Stethoscope },
  { id: 'import', icon: Import },
];

const TOOL_PAGES: { id: ToolPage; labelKey: string; keywords: string }[] = [
  {
    id: 'agents',
    labelKey: 'config.tabAgents',
    keywords: 'agents subagents 子代理 智能体',
  },
  { id: 'skills', labelKey: 'config.tabSkills', keywords: 'skills 技能' },
  { id: 'plugins', labelKey: 'config.tabPlugins', keywords: 'plugins 插件' },
  {
    id: 'automations',
    labelKey: 'sidebar.automations',
    keywords: 'automations schedule cron 自动化 定时',
  },
];

const THEMES: { id: 'dark' | 'light' | 'auto'; labelKey: string }[] = [
  { id: 'dark', labelKey: 'config.uiThemeDark' },
  { id: 'light', labelKey: 'config.uiThemeLight' },
  { id: 'auto', labelKey: 'config.uiThemeAuto' },
];

export function buildCommands({
  t,
  actions,
  state,
}: CommandSources): Command[] {
  const commands: Command[] = [];
  const workspaceName = (path: string) =>
    state.workspaces.find((w) => w.path === path)?.title || path;

  for (const tab of SETTINGS_TABS) {
    const label = tab.id.charAt(0).toUpperCase() + tab.id.slice(1);
    commands.push({
      id: `settings:${tab.id}`,
      group: 'palette.groupSettings',
      title: t(`config.tab${label}`),
      hint: t('config.title'),
      keywords: `settings preferences 设置 偏好 ${tab.id}`,
      // ⌘, opens the page on this tab.
      shortcut: tab.id === 'general' ? 'settings.open' : undefined,
      icon: tab.icon,
      run: () => actions.openConfig(tab.id),
    });
  }

  for (const page of TOOL_PAGES) {
    commands.push({
      id: `tools:${page.id}`,
      group: 'palette.groupNav',
      title: t(page.labelKey),
      hint: t('palette.toolsPage'),
      keywords: page.keywords,
      icon: ToolIcons[page.id],
      run: () => actions.openTools(page.id),
    });
  }

  commands.push(
    {
      id: 'nav:new-chat',
      group: 'palette.groupActions',
      title: t('palette.newChat'),
      hint: state.workspace ? workspaceName(state.workspace) : undefined,
      keywords: 'new chat session 新建 对话 会话',
      shortcut: 'chat.new',
      icon: SquarePen,
      run: () => actions.newChat(),
    },
    {
      id: 'nav:files',
      group: 'palette.groupNav',
      title: t('palette.browseFiles'),
      keywords: 'files tree explorer 文件 目录 浏览',
      shortcut: 'panel.files',
      icon: FileText,
      run: () => actions.openFiles(),
    },
    {
      id: 'nav:workspace',
      group: 'palette.groupActions',
      title: t('palette.chooseWorkspace'),
      keywords: 'open folder workspace 打开 文件夹 工作区',
      icon: FolderOpen,
      run: () => actions.chooseWorkspace(),
    },
  );

  // The commands whose only other home is a key: listing them in the
  // palette is what makes them findable without the sheet.
  commands.push(
    {
      id: 'nav:shortcuts',
      group: 'palette.groupNav',
      title: t('shortcuts.title'),
      keywords: 'keys shortcuts keyboard bindings 快捷键 键盘 按键',
      shortcut: 'shortcuts.open',
      icon: Keyboard,
      run: () => actions.runShortcut('shortcuts.open'),
    },
    {
      id: 'chat:focus-composer',
      group: 'palette.groupActions',
      title: t('shortcuts.focusComposer'),
      keywords: 'focus composer input caret 聚焦 输入框 光标',
      shortcut: 'chat.focusComposer',
      icon: TextCursorInput,
      run: () => actions.runShortcut('chat.focusComposer'),
    },
    {
      id: 'chat:copy-reply',
      group: 'palette.groupActions',
      title: t('shortcuts.copyLastReply'),
      keywords: 'copy reply clipboard 复制 回复 剪贴板',
      shortcut: 'chat.copyLastReply',
      icon: Copy,
      run: () => actions.runShortcut('chat.copyLastReply'),
    },
  );

  for (const theme of THEMES) {
    if (theme.id === state.theme) continue;
    commands.push({
      id: `theme:${theme.id}`,
      group: 'palette.groupActions',
      title: t('palette.switchTheme', { theme: t(theme.labelKey) }),
      keywords: 'theme appearance dark light auto 主题 外观 深色 浅色',
      icon: Moon,
      run: () => actions.setTheme(theme.id),
    });
  }

  for (const ws of state.workspaces) {
    if (ws.path === state.workspace) continue;
    commands.push({
      id: `workspace:${ws.id}`,
      group: 'palette.groupWorkspaces',
      title: ws.title || ws.path,
      hint: ws.path,
      keywords: `workspace switch folder 工作区 切换 ${ws.path}`,
      icon: FolderOpen,
      run: () => actions.openWorkspace(ws.path),
    });
  }

  // Sessions are the longest list in the app; the palette offers the ones
  // the sidebar already ranked, not the whole archive. They belong to the
  // active workspace — the store only loads that workspace's sessions —
  // which is also the hint shown next to each row.
  for (const session of state.sessions.slice(0, 20)) {
    commands.push({
      id: `session:${session.id}`,
      group: 'palette.groupSessions',
      title: session.title || t('sidebar.newSession'),
      hint: state.workspace ? workspaceName(state.workspace) : undefined,
      keywords: 'session conversation chat 会话 对话',
      icon: MessageSquare,
      run: () => actions.openSessionInWorkspace(session.id, state.workspace),
    });
  }

  return commands;
}

const ToolIcons: Record<ToolPage, Command['icon']> = {
  agents: Bot,
  skills: Sparkles,
  plugins: Package,
  automations: CalendarClock,
};

/**
 * filterCommands ranks by how the query hits a command, keeping the
 * catalogue order as the tie-break so an empty query lists the palette in
 * the order the groups were built.
 */
export function filterCommands(commands: Command[], query: string): Command[] {
  const needle = query.trim().toLowerCase();
  if (needle === '') return commands;
  const scored: { command: Command; score: number }[] = [];
  for (const [index, command] of commands.entries()) {
    const score = scoreCommand(command, needle);
    if (score === 0) continue;
    scored.push({ command, score: score * 1000 + (commands.length - index) });
  }
  scored.sort((a, b) => b.score - a.score);
  return scored.map((entry) => entry.command);
}

function scoreCommand(command: Command, needle: string): number {
  const title = command.title.toLowerCase();
  const hint = (command.hint ?? '').toLowerCase();
  if (title === needle) return 100;
  if (title.startsWith(needle)) return 80;
  if (words(title).some((word) => word.startsWith(needle))) return 70;
  if (title.includes(needle)) return 60;
  if (hint.includes(needle)) return 40;
  if ((command.keywords ?? '').toLowerCase().includes(needle)) return 30;
  // Last resort: a subsequence ("ncht" → "New chat"), which is how a
  // half-remembered name still finds its command.
  return subsequence(title, needle) ? 10 : 0;
}

function words(value: string): string[] {
  return value.split(/[\s·/|-]+/).filter((word) => word !== '');
}

function subsequence(value: string, needle: string): boolean {
  let at = 0;
  for (const char of value) {
    if (char === needle[at]) at += 1;
    if (at === needle.length) return true;
  }
  return needle.length === 0;
}
