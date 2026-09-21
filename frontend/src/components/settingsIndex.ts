import type { ComponentType } from 'react';
import {
  BarChart3,
  Cpu,
  Database,
  Import,
  Palette,
  Stethoscope,
  ShieldCheck,
  SlidersHorizontal,
  Wrench,
} from 'lucide-react';

// The settings page is searchable over a static index: every entry names
// the tab it lives in and the anchor to scroll to (`section`), which the
// tab bodies render as an id (`settings-<section>`). A runtime scan of the
// DOM cannot replace it — only the active tab is mounted, so anything the
// user has not opened yet would be unfindable, which is exactly the case
// search exists for.
//
// Keywords are written in both languages on purpose: the app ships zh and
// en UI, users search in whichever language they read the UI in, and
// half-translated feature names ("OTLP", "MCP", "PATH") show up in both.
// When a tab grows an anchor (a card with an id) add the entry here —
// `section` must match `id="settings-<section>"` in the tab body.
export interface SettingsTabEntry {
  id: string;
  icon: ComponentType<{ size?: string | number; className?: string }>;
}

export interface SettingsSection {
  /** Tab the entry lives in. */
  tab: string;
  /** Anchor id suffix in that tab; absent for a tab-level entry. */
  section?: string;
  labelKey: string;
  keywords: string;
}

export const SETTINGS_TABS: SettingsTabEntry[] = [
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

export const SETTINGS_SECTIONS: SettingsSection[] = [
  {
    tab: 'general',
    labelKey: 'config.tabGeneral',
    keywords:
      'general session default sandbox yolo mode think pet notify startup ' +
      '通用 会话 默认 沙箱 模式 思考 通知 开机',
  },
  {
    tab: 'display',
    labelKey: 'config.tabDisplay',
    keywords:
      'display interface theme dark light auto font size scale zoom mono ' +
      'language pet character ' +
      '外观 界面 主题 深色 浅色 跟随系统 字体 字号 缩放 等宽 语言 宠物 形象',
  },
  {
    tab: 'inference',
    labelKey: 'config.tabInference',
    keywords:
      'inference model provider api key credential base url wire streaming ' +
      'reasoning router retry vision ' +
      '推理 模型 提供方 服务商 密钥 凭据 基地址 流式 推理强度 路由 重试 视觉',
  },
  {
    tab: 'inference',
    section: 'inference-instances',
    labelKey: 'config.secInferenceInstances',
    keywords:
      'instances models add remove endpoints wire template catalog ' +
      '实例 模型 添加 删除 端点 模板 目录',
  },
  {
    tab: 'inference',
    section: 'inference-router',
    labelKey: 'config.secInferenceRouter',
    keywords: 'router fallback attempts retry policy 路由 回退 重试 次数',
  },
  {
    tab: 'tools',
    labelKey: 'config.tabTools',
    keywords:
      'tools providers image video generation knobs ' +
      '工具 提供方 生图 视频 生成 开关',
  },
  {
    tab: 'tools',
    section: 'tools-websearch',
    labelKey: 'config.webSearchTitle',
    keywords:
      'web search tavily brave exa parallel mcp key max results freshness ' +
      '网页搜索 联网搜索 搜索 密钥 条数 时效',
  },
  {
    tab: 'tools',
    section: 'tools-mcp',
    labelKey: 'config.tabMCP',
    keywords:
      'mcp model context protocol server stdio http tools discovery ' +
      'mcp 服务器 工具发现 命令行',
  },
  {
    tab: 'memory',
    labelKey: 'config.tabMemory',
    keywords:
      'memory context window history replay fold compact summary budget ' +
      '记忆 上下文 窗口 历史 折叠 压缩 摘要 预算',
  },
  {
    tab: 'permissions',
    labelKey: 'config.tabPermissions',
    keywords:
      'permissions allowlist approvals sandbox escalation rules ' +
      '权限 白名单 批准 沙箱 升级 规则',
  },
  {
    tab: 'usage',
    labelKey: 'config.tabUsage',
    keywords:
      'usage tokens cost calls latency cache hit range export ' +
      '用量 令牌 费用 调用 延迟 缓存 命中 时间范围 导出',
  },
  {
    tab: 'diagnostics',
    labelKey: 'config.tabDiagnostics',
    keywords:
      'diagnostics health environment doctors logs ' +
      '诊断 环境 健康 日志 排查',
  },
  {
    tab: 'diagnostics',
    section: 'diag-runtime',
    labelKey: 'config.secDiagRuntime',
    keywords:
      'runtime version platform go node sandbox shell config inference probe cache reload ' +
      '运行时 版本 平台 沙箱 shell 配置 推理 探测 缓存 重载',
  },
  {
    tab: 'diagnostics',
    section: 'diag-policy',
    labelKey: 'config.secDiagPolicy',
    keywords: 'policy approval check command 策略 批准 检查 命令',
  },
  {
    tab: 'diagnostics',
    section: 'diag-recovery',
    labelKey: 'config.diagRecoveryTitle',
    keywords:
      'crash recovery interrupted restart checkpoint resume continue ' +
      '崩溃 恢复 中断 重启 检查点 继续',
  },
  {
    tab: 'diagnostics',
    section: 'diag-otlp',
    labelKey: 'config.diagTelemetryTitle',
    keywords:
      'otlp telemetry export traces metrics logs collector honeycomb ' +
      'otlp 遥测 导出 链路 指标 采集器',
  },
  {
    tab: 'diagnostics',
    section: 'diag-httpprobe',
    labelKey: 'config.diagHttpProbeTitle',
    keywords:
      'http probe round trip transport provider latency request headers ' +
      '请求 往返 探针 传输 延迟 首包',
  },
  {
    tab: 'diagnostics',
    section: 'diag-path',
    labelKey: 'config.diagPathTitle',
    keywords:
      'path environment prepend homebrew mcp spawn lookup ' +
      '路径 环境变量 前置 查找 启动',
  },
  {
    tab: 'diagnostics',
    section: 'diag-execpool',
    labelKey: 'config.diagExecPoolTitle',
    keywords:
      'exec pool prewarm idle active sandbox processes ' +
      '执行池 预启动 空闲 并发 进程',
  },
  {
    tab: 'diagnostics',
    section: 'diag-heap',
    labelKey: 'config.heapProfileTitle',
    keywords: 'heap profile memory pprof gc leak ' + '内存 快照 堆 分析 泄漏',
  },
  {
    tab: 'diagnostics',
    section: 'diag-perfprobe',
    labelKey: 'config.diagPerfProbeTitle',
    keywords:
      'renderer perf sampler dom nodes frame flush transcript ' +
      '渲染 性能 采样 dom 节点 卡顿 帧 刷新',
  },
  {
    tab: 'diagnostics',
    section: 'diag-pet',
    labelKey: 'config.petDiagTitle',
    keywords: 'pet behavior mood phase energy 宠物 行为 心情 阶段 精力',
  },
  {
    tab: 'diagnostics',
    section: 'diag-logs',
    labelKey: 'config.secDiagLogs',
    keywords: 'logs tail follow filter level copy 日志 跟随 过滤 级别 复制',
  },
  {
    tab: 'diagnostics',
    section: 'diag-metrics',
    labelKey: 'config.metricsTitle',
    keywords:
      'metrics performance gc memory heap turns startup chart ' +
      '指标 性能 gc 内存 堆 轮次 启动 曲线',
  },
  {
    tab: 'import',
    labelKey: 'config.tabImport',
    keywords:
      'import conversations sessions migrate other apps ' +
      '导入 会话 迁移 其他应用',
  },
];

/**
 * searchSettings ranks the index against a query, keeping the order above
 * when nothing matches better: a tab entry always outranks its own
 * sections, and prefix matches outrank substring matches, so typing "us"
 * offers the Usage tab before "Process PATH".
 */
export function searchSettings(
  query: string,
  label: (key: string) => string,
): SettingsSection[] {
  const needle = query.trim().toLowerCase();
  if (needle === '') return [];
  const scored: { entry: SettingsSection; score: number }[] = [];
  for (const [index, entry] of SETTINGS_SECTIONS.entries()) {
    const title = label(entry.labelKey).toLowerCase();
    const tabLabel = label(`config.tab${capitalize(entry.tab)}`).toLowerCase();
    let score = 0;
    if (title.startsWith(needle) || tabLabel.startsWith(needle)) score = 4;
    else if (title.includes(needle)) score = 3;
    else if (tabLabel.includes(needle)) score = 2;
    else if (entry.keywords.toLowerCase().includes(needle)) score = 1;
    if (score === 0) continue;
    // Tab-level entries (no anchor) outrank sections inside the tab.
    if (entry.section === undefined) score += 1;
    scored.push({ entry, score: score * 1000 - index });
  }
  scored.sort((a, b) => b.score - a.score);
  return scored.map((row) => row.entry);
}

function capitalize(value: string): string {
  return value.charAt(0).toUpperCase() + value.slice(1);
}
