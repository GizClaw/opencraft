import { memo, useEffect, useState } from 'react';
import {
  Bot,
  Check,
  ChevronDown,
  ChevronRight,
  ClipboardList,
  File,
  FileSearch,
  Folder,
  Globe,
  HelpCircle,
  Image as ImageIcon,
  Loader2,
  Puzzle,
  Search as SearchIcon,
  Send,
  ShieldCheck,
  Sparkles,
  Users,
  Wrench,
  X,
} from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { api } from '../lib/api';
import {
  looksLikeUnifiedDiff,
  parseUnifiedDiff,
  recoverJsonContent,
} from '../lib/diff';
import type { ToolView } from '../lib/store';
import type { PatchFileDTO, PatchLineDTO } from '../lib/types';

function parseArgs(tool: ToolView): Record<string, unknown> | null {
  try {
    const v = JSON.parse(tool.args);
    return v && typeof v === 'object' ? v : null;
  } catch {
    return null;
  }
}

interface Summary {
  verb: string;
  rest: string;
}

function summaryOf(tool: ToolView): Summary | null {
  const args = parseArgs(tool);
  if (!args) return null;
  const str = (v: unknown): string => (typeof v === 'string' ? v : '');
  switch (tool.name) {
    case 'exec_command':
    case 'exec_session':
      return {
        verb: 'ran',
        rest:
          str(args.command) ||
          (Array.isArray(args.argv) ? args.argv.join(' ') : ''),
      };
    case 'read_file':
      return { verb: 'read', rest: str(args.file_path) };
    case 'write_file':
      return { verb: 'wrote', rest: str(args.file_path) };
    case 'list_dir':
      return { verb: 'listed', rest: str(args.path) || '.' };
    case 'grep': {
      const parts = [str(args.pattern)];
      if (str(args.path)) parts.push(`in ${args.path}`);
      return { verb: 'grep', rest: parts.join(' ') };
    }
    case 'glob':
      return { verb: 'glob', rest: str(args.pattern) };
    case 'request_permissions':
      return {
        verb: 'requestPermissions',
        rest: Array.isArray(args.permissions)
          ? String(args.permissions.length)
          : '',
      };
    case 'update_plan':
      return { verb: 'updatePlan', rest: '' };
    case 'apply_patch':
      return { verb: 'patch', rest: '' };
    case 'skill_create':
      return { verb: 'createdSkill', rest: str(args.name) };
    case 'skill_modify':
      return { verb: 'modifiedSkill', rest: str(args.name) };
    case 'skill_install':
      return { verb: 'installedSkill', rest: str(args.name) || str(args.repo) };
    case 'web_fetch':
      return { verb: 'fetched', rest: str(args.url) };
    case 'generate_image':
      return { verb: 'generatedImage', rest: str(args.prompt) };
    case 'generate_video':
      return { verb: 'generatedVideo', rest: str(args.prompt) };
    case 'ask_user':
      return { verb: 'askUser', rest: str(args.question) };
    case 'skill_search':
      return { verb: 'searchedSkill', rest: str(args.query) };
    case 'skill_read':
      return { verb: 'readSkill', rest: str(args.name) };
    case 'create_agent':
      return { verb: 'createdAgent', rest: str(args.name) };
    case 'update_agent':
      return { verb: 'updatedAgent', rest: str(args.name) };
    case 'unregister_agent':
      return { verb: 'unregisteredAgent', rest: str(args.name) };
    case 'tool_search':
      return { verb: 'searchedTools', rest: str(args.query) };
    default:
      return null;
  }
}

interface ExecResult {
  exit_code?: number;
  stdout?: string;
  stderr?: string;
}

function execResult(content: string): ExecResult | null {
  try {
    const v = JSON.parse(content);
    // Only exec_command / exec_session results carry exit_code; other
    // tools return JSON objects too (read_file, apply_patch, ...) and
    // must not be treated as exec output (that rendered "exit code
    // undefined" in the expanded card).
    if (v && typeof v === 'object' && typeof v.exit_code === 'number') {
      return v;
    }
  } catch {
    // fall through
  }
  return null;
}

interface DirEntry {
  path: string;
  type: string;
  size?: number;
}

interface DirResult {
  path?: string;
  entries?: DirEntry[];
  truncated?: boolean;
}

interface DirNode {
  name: string;
  type: string;
  size?: number;
  children: DirNode[];
}

// parseDirResult turns a list_dir result into a nested tree. Entries
// arrive as flat paths (e.g. "src/util/x.go"), so parent directories
// are synthesized when the tool only listed files at depth.
function parseDirResult(content: string): DirResult | null {
  try {
    const v = JSON.parse(content);
    if (v && typeof v === 'object' && Array.isArray(v.entries)) {
      return v as DirResult;
    }
  } catch {
    // not JSON
  }
  return null;
}

function buildDirTree(entries: DirEntry[]): DirNode[] {
  const root: DirNode[] = [];
  for (const entry of entries) {
    const parts = entry.path.split('/').filter(Boolean);
    if (parts.length === 0) continue;
    let level = root;
    parts.forEach((part, i) => {
      const isLast = i === parts.length - 1;
      let node = level.find((n) => n.name === part);
      if (!node) {
        node = {
          name: part,
          type: isLast ? entry.type : 'dir',
          size: isLast ? entry.size : undefined,
          children: [],
        };
        level.push(node);
      } else if (isLast) {
        node.type = entry.type;
        node.size = entry.size;
      }
      level = node.children;
    });
  }
  const sort = (nodes: DirNode[]) => {
    nodes.sort((a, b) => {
      if (a.type !== b.type) return a.type === 'dir' ? -1 : 1;
      return a.name.localeCompare(b.name);
    });
    nodes.forEach((n) => sort(n.children));
  };
  sort(root);
  return root;
}

function fmtSize(n?: number): string {
  if (n === undefined || n < 0) return '';
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}k`;
  return n > 0 ? `${n}B` : '';
}

// tailLine returns the last non-empty line of text, trimmed for use in
// the collapsed header summary.
function tailLine(text: string): string {
  const line =
    text
      .trimEnd()
      .split('\n')
      .map((l) => l.trim())
      .filter(Boolean)
      .pop() ?? '';
  return line.length > 120 ? `${line.slice(0, 120)}…` : line;
}

// ExecView renders exec_command / exec_session as a standalone
// collapsible terminal-style block: the header shows the command,
// status, exit code and output tail; expanding reveals stdout / stderr.
function ExecView({ tool }: { tool: ToolView }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const [copied, setCopied] = useState(false);
  const args = parseArgs(tool);
  const command =
    args && typeof args.command === 'string'
      ? args.command
      : args && Array.isArray(args.argv)
        ? args.argv.join(' ')
        : '';
  const exec = tool.result !== undefined ? execResult(tool.result) : null;
  const running = tool.status === 'running';
  const failed =
    tool.status === 'error' || (exec !== null && exec.exit_code !== 0);
  const stdout = exec?.stdout ?? '';
  const stderr = exec?.stderr ?? '';
  const stdoutLines = stdout ? stdout.split('\n').length : 0;
  const stderrLines = stderr ? stderr.split('\n').length : 0;
  const hasOutput = Boolean(stdout || stderr);
  const summaryTail = failed
    ? tailLine(stderr || stdout)
    : tailLine(stdout || stderr);

  const copyOutput = async () => {
    const text = [stderr, stdout].filter(Boolean).join('\n');
    if (!text) return;
    try {
      await navigator.clipboard.writeText(text);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // clipboard unavailable
    }
  };

  return (
    <div className="my-1.5">
      <button
        onClick={() => setOpen(!open)}
        className={`flex w-full items-center gap-2 rounded-lg border px-3 py-2 text-left text-sm transition-colors ${
          running
            ? 'border-accent/40 bg-panel2'
            : failed
              ? 'border-err/40 bg-err/5'
              : 'border-edge bg-panel2'
        } hover:bg-panel2/70`}
      >
        {running ? (
          <Loader2
            size="1.0000rem"
            className="animate-spin shrink-0 text-accent"
          />
        ) : failed ? (
          <X size="1.0000rem" className="shrink-0 text-err" />
        ) : (
          <Check size="1.0000rem" className="shrink-0 text-ok" />
        )}
        <span className="min-w-0 flex-1 truncate font-mono text-xs text-fg">
          <span className="select-none text-dim">$ </span>
          {command || tool.name}
        </span>
        {!running && exec !== null && (
          <span
            className={`shrink-0 rounded px-1.5 py-0.5 font-mono text-[0.7143rem] ${
              failed ? 'bg-err/10 text-err' : 'bg-ok/10 text-ok'
            }`}
          >
            {t('tool.exit')} {exec.exit_code}
          </span>
        )}
        {hasOutput && !running && (
          <span
            className={`shrink-0 truncate font-mono text-[0.7143rem] max-w-[40%] ${
              failed ? 'text-err' : 'text-ok'
            }`}
          >
            {summaryTail}
          </span>
        )}
        {hasOutput && (
          <button
            onClick={(e) => {
              e.stopPropagation();
              void copyOutput();
            }}
            className="shrink-0 rounded border border-edge px-1.5 py-0.5 text-[0.7143rem] text-dim hover:text-fg"
            aria-label={t('tool.copyResult')}
          >
            {copied ? (
              <Check size="0.7857rem" />
            ) : (
              <ClipboardList size="0.7857rem" />
            )}
          </button>
        )}
        {open ? (
          <ChevronDown size="1.0000rem" className="shrink-0 text-dim" />
        ) : (
          <ChevronRight size="1.0000rem" className="shrink-0 text-dim" />
        )}
      </button>
      {open && (
        <div className="mt-1 overflow-hidden rounded-md border border-edge bg-panel/60">
          {running && (
            <div className="flex items-center gap-1.5 px-2.5 py-2 text-xs text-dim">
              <Loader2 size="0.8571rem" className="animate-spin" />
              {t('tool.running')}
            </div>
          )}
          {!running && hasOutput && (
            <div className="max-h-72 overflow-y-auto px-2.5 py-2 space-y-1.5">
              {stderr && (
                <div>
                  <div className="mb-0.5 flex items-center gap-2 text-[0.7143rem] text-dim">
                    <span className="uppercase tracking-wider">stderr</span>
                    <span className="tabular-nums">{stderrLines}</span>
                  </div>
                  <pre className="whitespace-pre-wrap break-all font-mono text-xs text-err">
                    {stderr}
                  </pre>
                </div>
              )}
              {stdout && (
                <div>
                  <div className="mb-0.5 flex items-center gap-2 text-[0.7143rem] text-dim">
                    <span className="uppercase tracking-wider">stdout</span>
                    <span className="tabular-nums">{stdoutLines}</span>
                  </div>
                  <pre className="whitespace-pre-wrap break-all font-mono text-xs text-fg">
                    {stdout}
                  </pre>
                </div>
              )}
            </div>
          )}
          {!running && !hasOutput && (
            <div className="px-2.5 py-2 text-xs text-dim">
              {failed ? t('tool.noOutput') : t('tool.completed')}
            </div>
          )}
        </div>
      )}
    </div>
  );
}

// ReadView renders read_file as a standalone collapsible block: the
// header shows the path and line/size summary; expanding reveals the
// file content in a scrollable code area.
function ReadView({ tool }: { tool: ToolView }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const [copied, setCopied] = useState(false);
  const args = parseArgs(tool);
  const argPath =
    args && typeof args.file_path === 'string' ? args.file_path : '';
  const parsed =
    tool.result !== undefined
      ? (() => {
          try {
            const v = JSON.parse(tool.result);
            if (v && typeof v === 'object' && typeof v.content === 'string') {
              return v as {
                file_path?: string;
                content: string;
                bytes?: number;
              };
            }
          } catch {
            // not JSON
          }
          return null;
        })()
      : null;
  const path = parsed?.file_path || argPath || tool.name;
  // Corrupt old archives sometimes break the result JSON (a raw
  // newline inside the envelope); recover the content leniently so the
  // card still renders instead of falling back to raw red JSON.
  const content =
    parsed?.content ??
    (tool.result !== undefined ? recoverJsonContent(tool.result) : '') ??
    '';
  const diffFiles = content ? parseUnifiedDiff(content) : null;
  const lines = content ? content.split('\n').length : 0;
  const running = tool.status === 'running';
  const failed = tool.status === 'error';

  const copyContent = async () => {
    if (!content) return;
    try {
      await navigator.clipboard.writeText(content);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // clipboard unavailable
    }
  };

  return (
    <div className="my-1.5">
      <button
        onClick={() => setOpen(!open)}
        className={`flex w-full items-center gap-2 rounded-lg border px-3 py-2 text-left text-sm transition-colors ${
          failed
            ? 'border-err/40 bg-err/5'
            : running
              ? 'border-accent/40 bg-panel2'
              : 'border-edge bg-panel2'
        } hover:bg-panel2/70`}
      >
        {running ? (
          <Loader2
            size="1.0000rem"
            className="animate-spin shrink-0 text-accent"
          />
        ) : failed ? (
          <X size="1.0000rem" className="shrink-0 text-err" />
        ) : (
          <File size="1.0000rem" className="shrink-0 text-dim" />
        )}
        <span className="min-w-0 flex-1 truncate font-mono text-xs text-fg">
          {path}
        </span>
        {parsed !== null && !running && (
          <span className="shrink-0 rounded bg-panel px-1.5 py-0.5 font-mono text-[0.7143rem] text-dim tabular-nums">
            {lines} {t('tool.lines')}
            {typeof parsed.bytes === 'number'
              ? ` · ${fmtSize(parsed.bytes)}`
              : ''}
          </span>
        )}
        {content && (
          <button
            onClick={(e) => {
              e.stopPropagation();
              void copyContent();
            }}
            className="shrink-0 rounded border border-edge px-1.5 py-0.5 text-[0.7143rem] text-dim hover:text-fg"
            aria-label={t('tool.copyResult')}
          >
            {copied ? (
              <Check size="0.7857rem" />
            ) : (
              <ClipboardList size="0.7857rem" />
            )}
          </button>
        )}
        {open ? (
          <ChevronDown size="1.0000rem" className="shrink-0 text-dim" />
        ) : (
          <ChevronRight size="1.0000rem" className="shrink-0 text-dim" />
        )}
      </button>
      {open && (
        <div className="mt-1 overflow-hidden rounded-md border border-edge bg-panel/60">
          {running && (
            <div className="flex items-center gap-1.5 px-2.5 py-2 text-xs text-dim">
              <Loader2 size="0.8571rem" className="animate-spin" />
              {t('tool.running')}
            </div>
          )}
          {!running && parsed === null && (
            <div className="px-2.5 py-2 text-xs text-err">{tool.result}</div>
          )}
          {!running && parsed !== null && diffFiles && (
            <GitDiffView files={diffFiles} maxHeight="max-h-72" />
          )}
          {!running && parsed !== null && !diffFiles && (
            <pre className="max-h-72 overflow-y-auto whitespace-pre-wrap break-all px-2.5 py-2 font-mono text-xs text-fg">
              {content}
            </pre>
          )}
        </div>
      )}
    </div>
  );
}

// WriteView renders write_file in full: the new content is always
// visible as a git-style diff scrolled to a ~10-line viewport, with a
// lightweight status line only while running or on failure.
export const WriteView = memo(function WriteView({ tool }: { tool: ToolView }) {
  const { t } = useTranslation();
  const args = parseArgs(tool);
  const path = args && typeof args.file_path === 'string' ? args.file_path : '';
  const content = args && typeof args.content === 'string' ? args.content : '';
  const running = tool.status === 'running';
  const failed = tool.status === 'error';
  const lines = content.split('\n').length;

  return (
    <div className="my-1.5 space-y-1">
      {running && (
        <div className="flex items-center gap-1.5 px-2.5 py-2 text-xs text-dim">
          <Loader2 size="0.8571rem" className="animate-spin" />
          {t('tool.running')}
        </div>
      )}
      {!running && failed && (
        <div className="px-2.5 py-2 text-xs text-err">{tool.result}</div>
      )}
      {!running && !failed && content && (
        <GitDiffView
          files={[
            {
              path,
              action: 'add',
              added: lines,
              removed: 0,
              lines: content.split('\n').map((text, i) => ({
                kind: 'add' as const,
                old_num: 0,
                new_num: i + 1,
                text,
              })),
            },
          ]}
          collapsible={false}
          maxHeight="max-h-[12.5rem]"
        />
      )}
    </div>
  );
});

// AskUserView renders ask_user as a standalone collapsible block: the
// header shows the question and the user's answer once provided;
// expanding reveals the question and available options.
function AskUserView({ tool }: { tool: ToolView }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const args = parseArgs(tool);
  const question =
    args && typeof args.question === 'string' ? args.question : tool.name;
  const kind = args && typeof args.kind === 'string' ? args.kind : 'text';
  const options =
    args && Array.isArray(args.options)
      ? args.options.filter((o): o is string => typeof o === 'string')
      : [];
  const parsed =
    tool.result !== undefined
      ? (() => {
          try {
            const v = JSON.parse(tool.result);
            if (v && typeof v === 'object') {
              return v as {
                cancelled?: boolean;
                choice?: string;
                choices?: string[];
                other?: string;
                text?: string;
              };
            }
          } catch {
            // not JSON
          }
          return null;
        })()
      : null;
  const running = tool.status === 'running';
  const failed = tool.status === 'error';
  const answer =
    parsed?.other ||
    parsed?.choice ||
    parsed?.text ||
    (parsed?.choices && parsed.choices.length > 0
      ? parsed.choices.join(', ')
      : '');

  return (
    <div className="my-1.5">
      <button
        onClick={() => setOpen(!open)}
        className={`flex w-full items-center gap-2 rounded-lg border px-3 py-2 text-left text-sm transition-colors ${
          failed
            ? 'border-err/40 bg-err/5'
            : running
              ? 'border-accent/40 bg-panel2'
              : 'border-edge bg-panel2'
        } hover:bg-panel2/70`}
      >
        {running ? (
          <Loader2
            size="1.0000rem"
            className="animate-spin shrink-0 text-accent"
          />
        ) : failed ? (
          <X size="1.0000rem" className="shrink-0 text-err" />
        ) : (
          <HelpCircle size="1.0000rem" className="shrink-0 text-warn" />
        )}
        <span className="min-w-0 flex-1 truncate text-sm text-fg">
          {question}
        </span>
        {!running && parsed !== null && (
          <span
            className={`shrink-0 rounded px-1.5 py-0.5 text-[0.7143rem] ${
              parsed.cancelled ? 'bg-panel text-dim' : 'bg-ok/10 text-ok'
            }`}
          >
            {parsed.cancelled
              ? t('tool.cancelled')
              : answer
                ? `✓ ${answer}`
                : t('tool.answered')}
          </span>
        )}
        {open ? (
          <ChevronDown size="1.0000rem" className="shrink-0 text-dim" />
        ) : (
          <ChevronRight size="1.0000rem" className="shrink-0 text-dim" />
        )}
      </button>
      {open && (
        <div className="mt-1 overflow-hidden rounded-md border border-edge bg-panel/60 px-2.5 py-2 space-y-1.5">
          {running && (
            <div className="flex items-center gap-1.5 text-xs text-dim">
              <Loader2 size="0.8571rem" className="animate-spin" />
              {t('tool.waitingAnswer')}
            </div>
          )}
          {!running && failed && (
            <div className="text-xs text-err">{tool.result}</div>
          )}
          {!running && !failed && (
            <>
              <div className="text-xs">
                <span className="text-dim">{t('tool.question')}: </span>
                <span className="text-fg">{question}</span>
              </div>
              {kind !== 'text' && options.length > 0 && (
                <div className="flex flex-wrap gap-1">
                  {options.map((o) => {
                    const chosen =
                      parsed?.choice === o ||
                      (Array.isArray(parsed?.choices) &&
                        parsed.choices.includes(o)) ||
                      (parsed?.text === o && o !== parsed?.other);
                    return (
                      <span
                        key={o}
                        className={`rounded border px-1.5 py-0.5 text-[0.7143rem] ${
                          chosen
                            ? 'border-ok/40 bg-ok/10 text-ok'
                            : 'border-edge bg-panel text-dim'
                        }`}
                      >
                        {o}
                      </span>
                    );
                  })}
                </div>
              )}
              {parsed !== null && !parsed.cancelled && answer && (
                <div className="flex items-start gap-1.5 border-t border-edge pt-1.5 text-xs">
                  <span className="text-dim">{t('tool.answer')}:</span>
                  <span className="text-ok">{answer}</span>
                </div>
              )}
              {parsed?.cancelled && (
                <div className="border-t border-edge pt-1.5 text-xs text-dim">
                  {t('tool.cancelled')}
                </div>
              )}
            </>
          )}
        </div>
      )}
    </div>
  );
}

// SkillSearchView renders skill_search as a standalone collapsible
// block: the header shows the query and hit count; expanding reveals
// each skill with its description and scope.
function SkillSearchView({ tool }: { tool: ToolView }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const args = parseArgs(tool);
  const query = args && typeof args.query === 'string' ? args.query : '';
  const hits =
    tool.result !== undefined
      ? (() => {
          try {
            const v = JSON.parse(tool.result);
            if (Array.isArray(v)) {
              return v as {
                name?: string;
                description?: string;
                path?: string;
                scope?: string;
                score?: number;
              }[];
            }
          } catch {
            // not JSON
          }
          return null;
        })()
      : null;
  const running = tool.status === 'running';
  const failed = tool.status === 'error';

  return (
    <div className="my-1.5">
      <button
        onClick={() => setOpen(!open)}
        className={`flex w-full items-center gap-2 rounded-lg border px-3 py-2 text-left text-sm transition-colors ${
          failed
            ? 'border-err/40 bg-err/5'
            : running
              ? 'border-accent/40 bg-panel2'
              : 'border-edge bg-panel2'
        } hover:bg-panel2/70`}
      >
        {running ? (
          <Loader2
            size="1.0000rem"
            className="animate-spin shrink-0 text-accent"
          />
        ) : failed ? (
          <X size="1.0000rem" className="shrink-0 text-err" />
        ) : (
          <Sparkles size="1.0000rem" className="shrink-0 text-accent" />
        )}
        <span className="min-w-0 flex-1 truncate text-sm text-fg">
          {t('tool.searchSkills')}
          {query && <span className="text-dim">: {query}</span>}
        </span>
        {hits !== null && !running && (
          <span className="shrink-0 rounded bg-panel px-1.5 py-0.5 font-mono text-[0.7143rem] text-dim tabular-nums">
            {hits.length} {t('tool.hits')}
          </span>
        )}
        {open ? (
          <ChevronDown size="1.0000rem" className="shrink-0 text-dim" />
        ) : (
          <ChevronRight size="1.0000rem" className="shrink-0 text-dim" />
        )}
      </button>
      {open && (
        <div className="mt-1 overflow-hidden rounded-md border border-edge bg-panel/60 px-2.5 py-2">
          {running && (
            <div className="flex items-center gap-1.5 text-xs text-dim">
              <Loader2 size="0.8571rem" className="animate-spin" />
              {t('tool.running')}
            </div>
          )}
          {!running && failed && (
            <div className="text-xs text-err">{tool.result}</div>
          )}
          {!running && hits !== null && hits.length === 0 && (
            <div className="text-xs text-dim">{t('tool.noHits')}</div>
          )}
          {!running && hits !== null && hits.length > 0 && (
            <div className="space-y-1.5">
              {hits.map((h) => (
                <div
                  key={h.name}
                  className="rounded-md border border-edge bg-panel px-2 py-1.5"
                >
                  <div className="flex items-center gap-1.5">
                    <Sparkles
                      size="0.8571rem"
                      className="shrink-0 text-accent"
                    />
                    <span className="font-mono text-xs text-fg">{h.name}</span>
                    {typeof h.score === 'number' && h.score > 0 && (
                      <span className="shrink-0 rounded bg-panel2 px-1 py-0.5 font-mono text-[0.7143rem] text-dim tabular-nums">
                        {h.score.toFixed(3)}
                      </span>
                    )}
                    {h.scope && (
                      <span className="shrink-0 rounded bg-panel2 px-1 py-0.5 text-[0.7143rem] text-dim">
                        {h.scope}
                      </span>
                    )}
                  </div>
                  {h.description && (
                    <div className="mt-0.5 text-xs text-dim">
                      {h.description}
                    </div>
                  )}
                  {h.path && (
                    <div className="mt-0.5 truncate font-mono text-[0.7143rem] text-dim">
                      {h.path}
                    </div>
                  )}
                </div>
              ))}
            </div>
          )}
        </div>
      )}
    </div>
  );
}

interface DelegationResult {
  delegation_id?: string;
  status?: string;
  output?: string;
  error?: string;
  metadata?: Record<string, string>;
}

// parseDelegationResult extracts the shared delegate / delegation_status
// response shape from a result string.
function parseDelegationResult(
  content: string | undefined,
): DelegationResult | null {
  if (content === undefined) return null;
  try {
    const v = JSON.parse(content);
    if (v && typeof v === 'object') {
      return v as DelegationResult;
    }
  } catch {
    // not JSON
  }
  return null;
}

function statusBadgeClass(status?: string): string {
  switch (status) {
    case 'succeeded':
      return 'bg-ok/10 text-ok';
    case 'failed':
      return 'bg-err/10 text-err';
    case 'canceled':
      return 'bg-panel text-dim';
    case 'running':
    case 'accepted':
      return 'bg-accent/10 text-accent';
    default:
      return 'bg-panel text-dim';
  }
}

// DelegateView renders delegate as a standalone collapsible block: the
// header shows the target and mode; expanding reveals the input and the
// delegated result (output / error).
function DelegateView({ tool }: { tool: ToolView }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const args = parseArgs(tool);
  const target = args && typeof args.target === 'string' ? args.target : '';
  const mode = args && typeof args.mode === 'string' ? args.mode : '';
  const input = args && typeof args.input === 'string' ? args.input : '';
  const result = parseDelegationResult(tool.result);
  const running = tool.status === 'running' && result === null;
  const failed = tool.status === 'error' || result?.status === 'failed';
  const terminal =
    result !== null &&
    result.status !== 'running' &&
    result.status !== 'accepted';
  const statusLabel = result?.status ? t(`tool.status.${result.status}`) : '';
  const output = result?.output ?? '';
  const error = result?.error ?? '';
  const metadata = result?.metadata ?? undefined;

  return (
    <div className="my-1.5">
      <button
        onClick={() => setOpen(!open)}
        className={`flex w-full items-center gap-2 rounded-lg border px-3 py-2 text-left text-sm transition-colors ${
          failed
            ? 'border-err/40 bg-err/5'
            : running
              ? 'border-accent/40 bg-panel2'
              : 'border-edge bg-panel2'
        } hover:bg-panel2/70`}
      >
        {running ? (
          <Loader2
            size="1.0000rem"
            className="animate-spin shrink-0 text-accent"
          />
        ) : failed ? (
          <X size="1.0000rem" className="shrink-0 text-err" />
        ) : (
          <Send size="1.0000rem" className="shrink-0 text-accent" />
        )}
        <span className="min-w-0 flex-1 truncate text-sm text-fg">
          {t('tool.delegated')}
          {target && (
            <>
              {' '}
              <code className="font-mono text-xs">{target}</code>
            </>
          )}
          {mode && (
            <span className="ml-1.5 rounded bg-panel px-1 py-0.5 align-middle text-[0.7143rem] text-dim">
              {mode}
            </span>
          )}
        </span>
        {result !== null && (
          <span
            className={`shrink-0 rounded px-1.5 py-0.5 text-[0.7143rem] ${statusBadgeClass(result.status)}`}
          >
            {statusLabel || result.status}
          </span>
        )}
        {terminal && (output || error) && (
          <span
            className={`hidden shrink-0 truncate font-mono text-[0.7143rem] max-w-[30%] lg:inline ${
              error ? 'text-err' : 'text-ok'
            }`}
          >
            {(error || output).slice(0, 120)}
          </span>
        )}
        {open ? (
          <ChevronDown size="1.0000rem" className="shrink-0 text-dim" />
        ) : (
          <ChevronRight size="1.0000rem" className="shrink-0 text-dim" />
        )}
      </button>
      {open && (
        <div className="mt-1 space-y-1.5 overflow-hidden rounded-md border border-edge bg-panel/60 px-2.5 py-2">
          {running && (
            <div className="flex items-center gap-1.5 text-xs text-dim">
              <Loader2 size="0.8571rem" className="animate-spin" />
              {t('tool.running')}
            </div>
          )}
          {!running && failed && tool.status === 'error' && (
            <div className="text-xs text-err">{tool.result}</div>
          )}
          {input && (
            <div className="text-xs">
              <span className="text-dim">{t('tool.input')}: </span>
              <span className="whitespace-pre-wrap text-fg">{input}</span>
            </div>
          )}
          {error && (
            <div className="text-xs">
              <span className="text-dim">{t('tool.error')}: </span>
              <span className="whitespace-pre-wrap text-err">{error}</span>
            </div>
          )}
          {output && (
            <div className="text-xs">
              <span className="text-dim">{t('tool.output')}: </span>
              <pre className="mt-0.5 max-h-48 overflow-y-auto whitespace-pre-wrap break-all font-mono text-fg">
                {output}
              </pre>
            </div>
          )}
          {metadata && Object.keys(metadata).length > 0 && (
            <div className="flex flex-wrap gap-1">
              {Object.entries(metadata).map(([k, v]) => (
                <span
                  key={k}
                  className="rounded border border-edge bg-panel px-1.5 py-0.5 text-[0.7143rem] text-dim"
                >
                  {k}: {v}
                </span>
              ))}
            </div>
          )}
          {!running && result !== null && !output && !error && (
            <div className="text-xs text-dim">
              {result.status === 'succeeded'
                ? t('tool.completed')
                : statusLabel || result.status}
            </div>
          )}
        </div>
      )}
    </div>
  );
}

// DelegationStatusView renders delegation_status as a standalone
// collapsible block: the header shows the delegation id and status;
// expanding reveals the latest output / error.
function DelegationStatusView({ tool }: { tool: ToolView }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const args = parseArgs(tool);
  const id =
    args && typeof args.delegation_id === 'string' ? args.delegation_id : '';
  const result = parseDelegationResult(tool.result);
  const running = tool.status === 'running' && result === null;
  const failed = tool.status === 'error' || result?.status === 'failed';
  const statusLabel = result?.status ? t(`tool.status.${result.status}`) : '';

  return (
    <div className="my-1.5">
      <button
        onClick={() => setOpen(!open)}
        className={`flex w-full items-center gap-2 rounded-lg border px-3 py-2 text-left text-sm transition-colors ${
          failed
            ? 'border-err/40 bg-err/5'
            : running
              ? 'border-accent/40 bg-panel2'
              : 'border-edge bg-panel2'
        } hover:bg-panel2/70`}
      >
        {running ? (
          <Loader2
            size="1.0000rem"
            className="animate-spin shrink-0 text-accent"
          />
        ) : failed ? (
          <X size="1.0000rem" className="shrink-0 text-err" />
        ) : (
          <Send size="1.0000rem" className="shrink-0 text-dim" />
        )}
        <span className="min-w-0 flex-1 truncate font-mono text-xs text-fg">
          {t('tool.delegationStatus')}
          {id && <span className="text-dim">: {id}</span>}
        </span>
        {result !== null && (
          <span
            className={`shrink-0 rounded px-1.5 py-0.5 text-[0.7143rem] ${statusBadgeClass(result.status)}`}
          >
            {statusLabel || result.status}
          </span>
        )}
        {open ? (
          <ChevronDown size="1.0000rem" className="shrink-0 text-dim" />
        ) : (
          <ChevronRight size="1.0000rem" className="shrink-0 text-dim" />
        )}
      </button>
      {open && (
        <div className="mt-1 space-y-1.5 overflow-hidden rounded-md border border-edge bg-panel/60 px-2.5 py-2">
          {running && (
            <div className="flex items-center gap-1.5 text-xs text-dim">
              <Loader2 size="0.8571rem" className="animate-spin" />
              {t('tool.running')}
            </div>
          )}
          {!running && tool.status === 'error' && (
            <div className="text-xs text-err">{tool.result}</div>
          )}
          {result?.error && (
            <div className="text-xs">
              <span className="text-dim">{t('tool.error')}: </span>
              <span className="whitespace-pre-wrap text-err">
                {result.error}
              </span>
            </div>
          )}
          {result?.output && (
            <div className="text-xs">
              <span className="text-dim">{t('tool.output')}: </span>
              <pre className="mt-0.5 max-h-48 overflow-y-auto whitespace-pre-wrap break-all font-mono text-fg">
                {result.output}
              </pre>
            </div>
          )}
          {result !== null && !result.output && !result.error && (
            <div className="text-xs text-dim">
              {statusLabel || result.status}
            </div>
          )}
        </div>
      )}
    </div>
  );
}

interface DelegationTarget {
  id?: string;
  description?: string;
  modes?: string[];
  metadata?: Record<string, string>;
}

// DelegationTargetsView renders delegation_targets as a standalone
// collapsible block: the header shows the target count; expanding
// reveals each target's id, description, and supported modes.
function DelegationTargetsView({ tool }: { tool: ToolView }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const targets =
    tool.result !== undefined
      ? (() => {
          try {
            const v = JSON.parse(tool.result);
            if (v && typeof v === 'object' && Array.isArray(v.targets)) {
              return v.targets as DelegationTarget[];
            }
          } catch {
            // not JSON
          }
          return null;
        })()
      : null;
  const running = tool.status === 'running';
  const failed = tool.status === 'error';

  return (
    <div className="my-1.5">
      <button
        onClick={() => setOpen(!open)}
        className={`flex w-full items-center gap-2 rounded-lg border px-3 py-2 text-left text-sm transition-colors ${
          failed
            ? 'border-err/40 bg-err/5'
            : running
              ? 'border-accent/40 bg-panel2'
              : 'border-edge bg-panel2'
        } hover:bg-panel2/70`}
      >
        {running ? (
          <Loader2
            size="1.0000rem"
            className="animate-spin shrink-0 text-accent"
          />
        ) : failed ? (
          <X size="1.0000rem" className="shrink-0 text-err" />
        ) : (
          <Users size="1.0000rem" className="shrink-0 text-accent" />
        )}
        <span className="min-w-0 flex-1 truncate text-sm text-fg">
          {t('tool.delegationTargets')}
        </span>
        {targets !== null && !running && (
          <span className="shrink-0 rounded bg-panel px-1.5 py-0.5 font-mono text-[0.7143rem] text-dim tabular-nums">
            {targets.length} {t('tool.targets')}
          </span>
        )}
        {open ? (
          <ChevronDown size="1.0000rem" className="shrink-0 text-dim" />
        ) : (
          <ChevronRight size="1.0000rem" className="shrink-0 text-dim" />
        )}
      </button>
      {open && (
        <div className="mt-1 space-y-1.5 overflow-hidden rounded-md border border-edge bg-panel/60 px-2.5 py-2">
          {running && (
            <div className="flex items-center gap-1.5 text-xs text-dim">
              <Loader2 size="0.8571rem" className="animate-spin" />
              {t('tool.running')}
            </div>
          )}
          {!running && failed && (
            <div className="text-xs text-err">{tool.result}</div>
          )}
          {!running && targets !== null && targets.length === 0 && (
            <div className="text-xs text-dim">{t('tool.noTargets')}</div>
          )}
          {!running && targets !== null && targets.length > 0 && (
            <div className="space-y-1.5">
              {targets.map((target) => (
                <div
                  key={target.id}
                  className="rounded-md border border-edge bg-panel px-2 py-1.5"
                >
                  <div className="flex flex-wrap items-center gap-1.5">
                    <span className="font-mono text-xs text-fg">
                      {target.id}
                    </span>
                    {Array.isArray(target.modes) &&
                      target.modes.map((m) => (
                        <span
                          key={m}
                          className="rounded bg-panel2 px-1 py-0.5 text-[0.7143rem] text-dim"
                        >
                          {m}
                        </span>
                      ))}
                  </div>
                  {target.description && (
                    <div className="mt-0.5 text-xs text-dim">
                      {target.description}
                    </div>
                  )}
                  {target.metadata &&
                    Object.keys(target.metadata).length > 0 && (
                      <div className="mt-1 flex flex-wrap gap-1">
                        {Object.entries(target.metadata).map(([k, v]) => (
                          <span
                            key={k}
                            className="rounded border border-edge bg-panel2 px-1 py-0.5 text-[0.7143rem] text-dim"
                          >
                            {k}: {v}
                          </span>
                        ))}
                      </div>
                    )}
                </div>
              ))}
            </div>
          )}
        </div>
      )}
    </div>
  );
}

// mcpNameParts splits an MCP tool name into its server and tool parts.
// MCP tools are namespaced "<server>__<tool>" by the SDK source.
function mcpNameParts(name: string): { server: string; tool: string } | null {
  const sep = name.indexOf('__');
  if (sep <= 0 || sep === name.length - 2) return null;
  const server = name.slice(0, sep);
  const tool = name.slice(sep + 2);
  if (!server || !tool) return null;
  return { server, tool };
}

// McpToolView renders MCP-backed tools as a standalone collapsible
// block: the header shows the server badge and tool name; expanding
// reveals the pretty-printed arguments and the rendered result.
function McpToolView({ tool }: { tool: ToolView }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const parts = mcpNameParts(tool.name);
  const args = parseArgs(tool);
  const running = tool.status === 'running';
  const failed = tool.status === 'error';
  const resultIsJson = (() => {
    if (tool.result === undefined) return null;
    try {
      return JSON.parse(tool.result) as unknown;
    } catch {
      return null;
    }
  })();
  const summaryTail =
    !running && !failed && tool.result !== undefined
      ? tailLine(tool.result)
      : '';

  return (
    <div className="my-1.5">
      <button
        onClick={() => setOpen(!open)}
        className={`flex w-full items-center gap-2 rounded-lg border px-3 py-2 text-left text-sm transition-colors ${
          failed
            ? 'border-err/40 bg-err/5'
            : running
              ? 'border-accent/40 bg-panel2'
              : 'border-edge bg-panel2'
        } hover:bg-panel2/70`}
      >
        {running ? (
          <Loader2
            size="1.0000rem"
            className="animate-spin shrink-0 text-accent"
          />
        ) : failed ? (
          <X size="1.0000rem" className="shrink-0 text-err" />
        ) : (
          <Puzzle size="1.0000rem" className="shrink-0 text-accent" />
        )}
        {parts && (
          <span className="shrink-0 rounded bg-panel px-1.5 py-0.5 font-mono text-[0.7143rem] text-accent">
            {parts.server}
          </span>
        )}
        <span className="min-w-0 flex-1 truncate font-mono text-xs text-fg">
          {parts ? parts.tool : tool.name}
        </span>
        {summaryTail && (
          <span className="hidden shrink-0 truncate font-mono text-[0.7143rem] max-w-[35%] text-dim lg:inline">
            {summaryTail}
          </span>
        )}
        {!running && tool.result !== undefined && (
          <span className="shrink-0 rounded bg-panel px-1.5 py-0.5 font-mono text-[0.7143rem] text-dim tabular-nums">
            {tool.result.split('\n').length} {t('tool.lines')}
          </span>
        )}
        {open ? (
          <ChevronDown size="1.0000rem" className="shrink-0 text-dim" />
        ) : (
          <ChevronRight size="1.0000rem" className="shrink-0 text-dim" />
        )}
      </button>
      {open && (
        <div className="mt-1 space-y-1.5 overflow-hidden rounded-md border border-edge bg-panel/60 px-2.5 py-2">
          {running && (
            <div className="flex items-center gap-1.5 text-xs text-dim">
              <Loader2 size="0.8571rem" className="animate-spin" />
              {t('tool.running')}
            </div>
          )}
          {!running && failed && (
            <div className="whitespace-pre-wrap break-all font-mono text-xs text-err">
              {tool.result}
            </div>
          )}
          {args && Object.keys(args).length > 0 && (
            <div>
              <div className="mb-0.5 text-[0.7143rem] uppercase tracking-wider text-dim">
                {t('tool.arguments')}
              </div>
              <pre className="max-h-48 overflow-y-auto whitespace-pre-wrap break-all rounded border border-edge bg-panel px-2 py-1.5 font-mono text-xs text-dim">
                {JSON.stringify(args, null, 2)}
              </pre>
            </div>
          )}
          {!running && !failed && tool.result !== undefined && (
            <div>
              <div className="mb-0.5 text-[0.7143rem] uppercase tracking-wider text-dim">
                {t('tool.result')}
              </div>
              {resultIsJson !== null ? (
                <pre className="max-h-72 overflow-y-auto whitespace-pre-wrap break-all rounded border border-edge bg-panel px-2 py-1.5 font-mono text-xs text-fg">
                  {JSON.stringify(resultIsJson, null, 2)}
                </pre>
              ) : (
                <pre className="max-h-72 overflow-y-auto whitespace-pre-wrap break-all rounded border border-edge bg-panel px-2 py-1.5 font-mono text-xs text-fg">
                  {tool.result}
                </pre>
              )}
            </div>
          )}
        </div>
      )}
    </div>
  );
}

interface GrepResult {
  matches?: { path?: string; line_number?: number; line?: string }[];
  truncated?: boolean;
  skipped_large?: number;
}

// GrepView renders grep as a standalone collapsible block: the header
// shows the pattern and match count; expanding reveals each match with
// its file and line number.
function GrepView({ tool }: { tool: ToolView }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const args = parseArgs(tool);
  const pattern = args && typeof args.pattern === 'string' ? args.pattern : '';
  const result =
    tool.result !== undefined
      ? (() => {
          try {
            const v = JSON.parse(tool.result);
            if (v && typeof v === 'object' && Array.isArray(v.matches)) {
              return v as GrepResult;
            }
          } catch {
            // not JSON
          }
          return null;
        })()
      : null;
  const running = tool.status === 'running';
  const failed = tool.status === 'error';
  const matches = result?.matches ?? [];

  return (
    <div className="my-1.5">
      <button
        onClick={() => setOpen(!open)}
        className={`flex w-full items-center gap-2 rounded-lg border px-3 py-2 text-left text-sm transition-colors ${
          failed
            ? 'border-err/40 bg-err/5'
            : running
              ? 'border-accent/40 bg-panel2'
              : 'border-edge bg-panel2'
        } hover:bg-panel2/70`}
      >
        {running ? (
          <Loader2
            size="1.0000rem"
            className="animate-spin shrink-0 text-accent"
          />
        ) : failed ? (
          <X size="1.0000rem" className="shrink-0 text-err" />
        ) : (
          <SearchIcon size="1.0000rem" className="shrink-0 text-accent" />
        )}
        <span className="min-w-0 flex-1 truncate font-mono text-xs text-fg">
          {pattern || tool.name}
        </span>
        {result !== null && !running && (
          <span className="shrink-0 rounded bg-panel px-1.5 py-0.5 font-mono text-[0.7143rem] text-dim tabular-nums">
            {matches.length} {t('tool.matches')}
            {result.truncated ? ' · …' : ''}
            {typeof result.skipped_large === 'number' &&
            result.skipped_large > 0
              ? ` · ${result.skipped_large} ${t('tool.skippedLarge')}`
              : ''}
          </span>
        )}
        {open ? (
          <ChevronDown size="1.0000rem" className="shrink-0 text-dim" />
        ) : (
          <ChevronRight size="1.0000rem" className="shrink-0 text-dim" />
        )}
      </button>
      {open && (
        <div className="mt-1 overflow-hidden rounded-md border border-edge bg-panel/60">
          {running && (
            <div className="flex items-center gap-1.5 px-2.5 py-2 text-xs text-dim">
              <Loader2 size="0.8571rem" className="animate-spin" />
              {t('tool.running')}
            </div>
          )}
          {!running && failed && (
            <div className="px-2.5 py-2 text-xs text-err">{tool.result}</div>
          )}
          {!running && result !== null && matches.length === 0 && (
            <div className="px-2.5 py-2 text-xs text-dim">
              {t('tool.noHits')}
            </div>
          )}
          {!running && matches.length > 0 && (
            <div className="max-h-72 space-y-0.5 overflow-y-auto px-2.5 py-2">
              {matches.map((m, i) => (
                <div
                  key={i}
                  className="flex items-start gap-2 font-mono text-xs"
                >
                  <span className="shrink-0 text-[0.7143rem] text-dim tabular-nums">
                    {m.line_number ?? ''}
                  </span>
                  <span className="shrink-0 text-[0.7143rem] text-accent">
                    {m.path ?? ''}
                  </span>
                  <span className="min-w-0 flex-1 truncate whitespace-pre text-fg">
                    {m.line ?? ''}
                  </span>
                </div>
              ))}
            </div>
          )}
        </div>
      )}
    </div>
  );
}

// GlobView renders glob as a standalone collapsible block: the header
// shows the pattern and match count; expanding reveals the paths.
function GlobView({ tool }: { tool: ToolView }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const args = parseArgs(tool);
  const pattern = args && typeof args.pattern === 'string' ? args.pattern : '';
  const matches =
    tool.result !== undefined
      ? (() => {
          try {
            const v = JSON.parse(tool.result);
            if (v && typeof v === 'object' && Array.isArray(v.matches)) {
              return v.matches as string[];
            }
          } catch {
            // not JSON
          }
          return null;
        })()
      : null;
  const running = tool.status === 'running';
  const failed = tool.status === 'error';

  return (
    <div className="my-1.5">
      <button
        onClick={() => setOpen(!open)}
        className={`flex w-full items-center gap-2 rounded-lg border px-3 py-2 text-left text-sm transition-colors ${
          failed
            ? 'border-err/40 bg-err/5'
            : running
              ? 'border-accent/40 bg-panel2'
              : 'border-edge bg-panel2'
        } hover:bg-panel2/70`}
      >
        {running ? (
          <Loader2
            size="1.0000rem"
            className="animate-spin shrink-0 text-accent"
          />
        ) : failed ? (
          <X size="1.0000rem" className="shrink-0 text-err" />
        ) : (
          <FileSearch size="1.0000rem" className="shrink-0 text-accent" />
        )}
        <span className="min-w-0 flex-1 truncate font-mono text-xs text-fg">
          {pattern || tool.name}
        </span>
        {matches !== null && !running && (
          <span className="shrink-0 rounded bg-panel px-1.5 py-0.5 font-mono text-[0.7143rem] text-dim tabular-nums">
            {matches.length} {t('tool.matches')}
          </span>
        )}
        {open ? (
          <ChevronDown size="1.0000rem" className="shrink-0 text-dim" />
        ) : (
          <ChevronRight size="1.0000rem" className="shrink-0 text-dim" />
        )}
      </button>
      {open && (
        <div className="mt-1 overflow-hidden rounded-md border border-edge bg-panel/60 px-2.5 py-2">
          {running && (
            <div className="flex items-center gap-1.5 text-xs text-dim">
              <Loader2 size="0.8571rem" className="animate-spin" />
              {t('tool.running')}
            </div>
          )}
          {!running && failed && (
            <div className="text-xs text-err">{tool.result}</div>
          )}
          {!running && matches !== null && matches.length === 0 && (
            <div className="text-xs text-dim">{t('tool.noHits')}</div>
          )}
          {!running && matches !== null && matches.length > 0 && (
            <div className="max-h-72 space-y-0.5 overflow-y-auto">
              {matches.map((m) => (
                <div key={m} className="truncate font-mono text-xs text-fg">
                  {m}
                </div>
              ))}
            </div>
          )}
        </div>
      )}
    </div>
  );
}

// PermissionsView renders request_permissions as a standalone
// collapsible block: the header shows the rule count and outcome;
// expanding reveals the requested rules and reason.
function PermissionsView({ tool }: { tool: ToolView }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const args = parseArgs(tool);
  const rules =
    args && Array.isArray(args.permissions)
      ? args.permissions.filter((p): p is string => typeof p === 'string')
      : [];
  const reason = args && typeof args.reason === 'string' ? args.reason : '';
  const parsed =
    tool.result !== undefined
      ? (() => {
          try {
            const v = JSON.parse(tool.result);
            if (v && typeof v === 'object') {
              return v as {
                granted?: boolean;
                scope?: string;
                permissions?: string[];
                cancelled?: boolean;
              };
            }
          } catch {
            // not JSON
          }
          return null;
        })()
      : null;
  const running = tool.status === 'running';
  const failed = tool.status === 'error';
  const outcome = parsed?.cancelled
    ? t('tool.cancelled')
    : parsed?.granted
      ? t('tool.granted')
      : parsed !== null
        ? t('tool.denied')
        : '';

  return (
    <div className="my-1.5">
      <button
        onClick={() => setOpen(!open)}
        className={`flex w-full items-center gap-2 rounded-lg border px-3 py-2 text-left text-sm transition-colors ${
          failed
            ? 'border-err/40 bg-err/5'
            : running
              ? 'border-accent/40 bg-panel2'
              : 'border-edge bg-panel2'
        } hover:bg-panel2/70`}
      >
        {running ? (
          <Loader2
            size="1.0000rem"
            className="animate-spin shrink-0 text-accent"
          />
        ) : failed ? (
          <X size="1.0000rem" className="shrink-0 text-err" />
        ) : (
          <ShieldCheck size="1.0000rem" className="shrink-0 text-accent" />
        )}
        <span className="min-w-0 flex-1 truncate text-sm text-fg">
          {t('tool.requestPermissions')}
          <span className="ml-1.5 rounded bg-panel px-1 py-0.5 align-middle text-[0.7143rem] text-dim tabular-nums">
            {rules.length}
          </span>
        </span>
        {outcome && (
          <span
            className={`shrink-0 rounded px-1.5 py-0.5 text-[0.7143rem] ${
              parsed?.granted
                ? 'bg-ok/10 text-ok'
                : parsed?.cancelled
                  ? 'bg-panel text-dim'
                  : 'bg-err/10 text-err'
            }`}
          >
            {outcome}
          </span>
        )}
        {open ? (
          <ChevronDown size="1.0000rem" className="shrink-0 text-dim" />
        ) : (
          <ChevronRight size="1.0000rem" className="shrink-0 text-dim" />
        )}
      </button>
      {open && (
        <div className="mt-1 space-y-1.5 overflow-hidden rounded-md border border-edge bg-panel/60 px-2.5 py-2">
          {running && (
            <div className="flex items-center gap-1.5 text-xs text-dim">
              <Loader2 size="0.8571rem" className="animate-spin" />
              {t('tool.waitingAnswer')}
            </div>
          )}
          {!running && failed && (
            <div className="text-xs text-err">{tool.result}</div>
          )}
          {rules.length > 0 && (
            <div className="space-y-0.5">
              {rules.map((rule) => (
                <div
                  key={rule}
                  className="flex items-center gap-1.5 font-mono text-xs"
                >
                  <span className="text-dim">$</span>
                  <span className="text-fg">{rule}</span>
                  {parsed?.granted &&
                    Array.isArray(parsed.permissions) &&
                    parsed.permissions.includes(rule) && (
                      <Check size="0.8571rem" className="shrink-0 text-ok" />
                    )}
                </div>
              ))}
            </div>
          )}
          {reason && (
            <div className="text-xs">
              <span className="text-dim">{t('tool.reason')}: </span>
              <span className="text-fg">{reason}</span>
            </div>
          )}
          {!running && parsed !== null && (
            <div className="border-t border-edge pt-1.5 text-xs">
              {parsed.cancelled ? (
                <span className="text-dim">{t('tool.cancelled')}</span>
              ) : parsed.granted ? (
                <span className="text-ok">
                  {t('tool.granted')}
                  {parsed.scope ? ` · ${parsed.scope}` : ''}
                </span>
              ) : (
                <span className="text-err">{t('tool.denied')}</span>
              )}
            </div>
          )}
        </div>
      )}
    </div>
  );
}

// SkillReadView renders skill_read as a standalone collapsible block:
// the header shows the skill name; expanding reveals the SKILL.md body.
function SkillReadView({ tool }: { tool: ToolView }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const args = parseArgs(tool);
  const name = args && typeof args.name === 'string' ? args.name : tool.name;
  const running = tool.status === 'running';
  const failed = tool.status === 'error';
  const content = tool.result ?? '';
  const lines = content ? content.split('\n').length : 0;

  return (
    <div className="my-1.5">
      <button
        onClick={() => setOpen(!open)}
        className={`flex w-full items-center gap-2 rounded-lg border px-3 py-2 text-left text-sm transition-colors ${
          failed
            ? 'border-err/40 bg-err/5'
            : running
              ? 'border-accent/40 bg-panel2'
              : 'border-edge bg-panel2'
        } hover:bg-panel2/70`}
      >
        {running ? (
          <Loader2
            size="1.0000rem"
            className="animate-spin shrink-0 text-accent"
          />
        ) : failed ? (
          <X size="1.0000rem" className="shrink-0 text-err" />
        ) : (
          <Sparkles size="1.0000rem" className="shrink-0 text-accent" />
        )}
        <span className="min-w-0 flex-1 truncate text-sm text-fg">
          {t('tool.readSkill')}{' '}
          <code className="font-mono text-xs">{name}</code>
        </span>
        {content && !running && (
          <span className="shrink-0 rounded bg-panel px-1.5 py-0.5 font-mono text-[0.7143rem] text-dim tabular-nums">
            {lines} {t('tool.lines')}
          </span>
        )}
        {open ? (
          <ChevronDown size="1.0000rem" className="shrink-0 text-dim" />
        ) : (
          <ChevronRight size="1.0000rem" className="shrink-0 text-dim" />
        )}
      </button>
      {open && (
        <div className="mt-1 overflow-hidden rounded-md border border-edge bg-panel/60">
          {running && (
            <div className="flex items-center gap-1.5 px-2.5 py-2 text-xs text-dim">
              <Loader2 size="0.8571rem" className="animate-spin" />
              {t('tool.running')}
            </div>
          )}
          {!running && failed && (
            <div className="px-2.5 py-2 text-xs text-err">{tool.result}</div>
          )}
          {!running && content && (
            <pre className="max-h-72 overflow-y-auto whitespace-pre-wrap break-all px-2.5 py-2 font-mono text-xs text-fg">
              {content}
            </pre>
          )}
        </div>
      )}
    </div>
  );
}

// WebFetchView renders web_fetch as a standalone collapsible block: the
// header shows the URL; expanding reveals title, description and body.
function WebFetchView({ tool }: { tool: ToolView }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const running = tool.status === 'running';
  useEffect(() => {
    if (running) setOpen(true);
  }, [running]);
  const args = parseArgs(tool);
  const url = args && typeof args.url === 'string' ? args.url : tool.name;
  const parsed =
    tool.result !== undefined
      ? (() => {
          try {
            const v = JSON.parse(tool.result);
            if (v && typeof v === 'object' && typeof v.content === 'string') {
              return v as {
                url?: string;
                title?: string;
                description?: string;
                site_name?: string;
                content?: string;
                content_type?: string;
                truncated?: boolean;
              };
            }
          } catch {
            // not JSON
          }
          return null;
        })()
      : null;
  const failed = tool.status === 'error';

  return (
    <div className="my-1.5">
      <button
        onClick={() => setOpen(!open)}
        className={`flex w-full items-center gap-2 rounded-lg border px-3 py-2 text-left text-sm transition-colors ${
          failed
            ? 'border-err/40 bg-err/5'
            : running
              ? 'border-accent/40 bg-panel2'
              : 'border-edge bg-panel2'
        } hover:bg-panel2/70`}
      >
        {running ? (
          <Loader2
            size="1.0000rem"
            className="animate-spin shrink-0 text-accent"
          />
        ) : failed ? (
          <X size="1.0000rem" className="shrink-0 text-err" />
        ) : (
          <Globe size="1.0000rem" className="shrink-0 text-accent" />
        )}
        <span className="min-w-0 flex-1 truncate text-sm text-fg">
          {parsed?.title || url}
        </span>
        {parsed?.site_name && !running && (
          <span className="shrink-0 rounded bg-panel px-1.5 py-0.5 text-[0.7143rem] text-dim">
            {parsed.site_name}
          </span>
        )}
        {parsed?.truncated && (
          <span className="shrink-0 text-[0.7143rem] text-warn">
            {t('tool.truncated')}
          </span>
        )}
        {open ? (
          <ChevronDown size="1.0000rem" className="shrink-0 text-dim" />
        ) : (
          <ChevronRight size="1.0000rem" className="shrink-0 text-dim" />
        )}
      </button>
      {open && (
        <div className="mt-1 space-y-1.5 overflow-hidden rounded-md border border-edge bg-panel/60 px-2.5 py-2">
          {running && (
            <div className="flex items-center gap-1.5 text-xs text-dim">
              <Loader2 size="0.8571rem" className="animate-spin" />
              {t('tool.running')}
            </div>
          )}
          {!running && failed && (
            <div className="whitespace-pre-wrap text-xs text-err">
              {tool.result}
            </div>
          )}
          {parsed !== null && (
            <>
              {parsed.title && (
                <div className="text-sm font-medium text-fg">
                  {parsed.title}
                </div>
              )}
              {parsed.description && (
                <div className="text-xs text-dim">{parsed.description}</div>
              )}
              {parsed.url && (
                <div className="truncate font-mono text-[0.7143rem] text-dim">
                  {parsed.url}
                </div>
              )}
              {parsed.content && (
                <pre className="max-h-72 overflow-y-auto whitespace-pre-wrap break-all rounded border border-edge bg-panel px-2 py-1.5 font-mono text-xs text-fg">
                  {parsed.content}
                </pre>
              )}
            </>
          )}
        </div>
      )}
    </div>
  );
}

// GenerateView renders generate_image / generate_video as a standalone
// collapsible block: the header shows a prompt preview; expanding
// reveals the generated artifact paths.
function GenerateView({ tool }: { tool: ToolView }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const running = tool.status === 'running';
  useEffect(() => {
    if (running) setOpen(true);
  }, [running]);
  const args = parseArgs(tool);
  const prompt = args && typeof args.prompt === 'string' ? args.prompt : '';
  const parsed =
    tool.result !== undefined
      ? (() => {
          try {
            const v = JSON.parse(tool.result);
            if (v && typeof v === 'object') {
              return v as {
                paths?: string[];
                count?: number;
                model?: string;
                hint?: string;
              };
            }
          } catch {
            // not JSON
          }
          return null;
        })()
      : null;
  const failed = tool.status === 'error';
  const paths = parsed?.paths ?? [];
  const isImage = tool.name === 'generate_image';

  return (
    <div className="my-1.5">
      <button
        onClick={() => setOpen(!open)}
        className={`flex w-full items-center gap-2 rounded-lg border px-3 py-2 text-left text-sm transition-colors ${
          failed
            ? 'border-err/40 bg-err/5'
            : running
              ? 'border-accent/40 bg-panel2'
              : 'border-edge bg-panel2'
        } hover:bg-panel2/70`}
      >
        {running ? (
          <Loader2
            size="1.0000rem"
            className="animate-spin shrink-0 text-accent"
          />
        ) : failed ? (
          <X size="1.0000rem" className="shrink-0 text-err" />
        ) : (
          <ImageIcon size="1.0000rem" className="shrink-0 text-accent" />
        )}
        <span className="min-w-0 flex-1 truncate text-sm text-fg">
          {prompt ||
            (isImage ? t('tool.generateImage') : t('tool.generateVideo'))}
        </span>
        {!running && parsed !== null && (
          <span className="shrink-0 rounded bg-panel px-1.5 py-0.5 font-mono text-[0.7143rem] text-dim tabular-nums">
            {paths.length} {t('tool.files')}
          </span>
        )}
        {open ? (
          <ChevronDown size="1.0000rem" className="shrink-0 text-dim" />
        ) : (
          <ChevronRight size="1.0000rem" className="shrink-0 text-dim" />
        )}
      </button>
      {open && (
        <div className="mt-1 space-y-1.5 overflow-hidden rounded-md border border-edge bg-panel/60 px-2.5 py-2">
          {running && (
            <div className="flex items-center gap-1.5 text-xs text-dim">
              <Loader2 size="0.8571rem" className="animate-spin" />
              {t('tool.running')}
            </div>
          )}
          {!running && failed && (
            <div className="whitespace-pre-wrap text-xs text-err">
              {tool.result}
            </div>
          )}
          {parsed !== null && (
            <>
              {paths.length > 0 && (
                <div className="space-y-0.5">
                  {paths.map((p) => (
                    <div key={p} className="truncate font-mono text-xs text-fg">
                      {p}
                    </div>
                  ))}
                </div>
              )}
              {parsed.model && (
                <div className="text-xs">
                  <span className="text-dim">{t('tool.model')}: </span>
                  <code className="font-mono text-fg">{parsed.model}</code>
                </div>
              )}
              {parsed.hint && (
                <div className="text-[0.7143rem] text-dim">{parsed.hint}</div>
              )}
            </>
          )}
        </div>
      )}
    </div>
  );
}

// SkillManageView renders skill_install / skill_create / skill_modify
// as a standalone collapsible block with a one-line header and the
// plain-text result.
function SkillManageView({ tool }: { tool: ToolView }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const args = parseArgs(tool);
  const name = args && typeof args.name === 'string' ? args.name : '';
  const repo = args && typeof args.repo === 'string' ? args.repo : '';
  const scope = args && typeof args.scope === 'string' ? args.scope : '';
  const running = tool.status === 'running';
  const failed = tool.status === 'error';
  const title =
    tool.name === 'skill_install'
      ? t('tool.installedSkill')
      : tool.name === 'skill_create'
        ? t('tool.createdSkill')
        : t('tool.modifiedSkill');
  const subject = name || repo;

  return (
    <div className="my-1.5">
      <button
        onClick={() => setOpen(!open)}
        className={`flex w-full items-center gap-2 rounded-lg border px-3 py-2 text-left text-sm transition-colors ${
          failed
            ? 'border-err/40 bg-err/5'
            : running
              ? 'border-accent/40 bg-panel2'
              : 'border-edge bg-panel2'
        } hover:bg-panel2/70`}
      >
        {running ? (
          <Loader2
            size="1.0000rem"
            className="animate-spin shrink-0 text-accent"
          />
        ) : failed ? (
          <X size="1.0000rem" className="shrink-0 text-err" />
        ) : (
          <Sparkles size="1.0000rem" className="shrink-0 text-accent" />
        )}
        <span className="min-w-0 flex-1 truncate text-sm text-fg">
          {title}
          {subject && (
            <code className="ml-1.5 font-mono text-xs">{subject}</code>
          )}
          {scope && (
            <span className="ml-1.5 rounded bg-panel px-1 py-0.5 align-middle text-[0.7143rem] text-dim">
              {scope}
            </span>
          )}
        </span>
        {open ? (
          <ChevronDown size="1.0000rem" className="shrink-0 text-dim" />
        ) : (
          <ChevronRight size="1.0000rem" className="shrink-0 text-dim" />
        )}
      </button>
      {open && (
        <div className="mt-1 overflow-hidden rounded-md border border-edge bg-panel/60 px-2.5 py-2">
          {running && (
            <div className="flex items-center gap-1.5 text-xs text-dim">
              <Loader2 size="0.8571rem" className="animate-spin" />
              {t('tool.running')}
            </div>
          )}
          {!running && failed && (
            <div className="whitespace-pre-wrap text-xs text-err">
              {tool.result}
            </div>
          )}
          {!running && !failed && tool.result !== undefined && (
            <pre className="whitespace-pre-wrap break-all font-mono text-xs text-fg">
              {tool.result}
            </pre>
          )}
        </div>
      )}
    </div>
  );
}

// AgentView renders create_agent / update_agent / unregister_agent as a
// standalone collapsible block: the header shows the agent name and the
// operation outcome; expanding reveals the result details.
function AgentView({ tool }: { tool: ToolView }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const args = parseArgs(tool);
  const name = args && typeof args.name === 'string' ? args.name : tool.name;
  const parsed =
    tool.result !== undefined
      ? (() => {
          try {
            const v = JSON.parse(tool.result);
            if (v && typeof v === 'object') {
              return v as {
                name?: string;
                description?: string;
                status?: string;
                persisted_to?: string;
                created_at?: string;
                hint?: string;
              };
            }
          } catch {
            // not JSON
          }
          return null;
        })()
      : null;
  const running = tool.status === 'running';
  const failed = tool.status === 'error';
  const verb =
    tool.name === 'create_agent'
      ? t('tool.createdAgent')
      : tool.name === 'update_agent'
        ? t('tool.updatedAgent')
        : t('tool.unregisteredAgent');

  return (
    <div className="my-1.5">
      <button
        onClick={() => setOpen(!open)}
        className={`flex w-full items-center gap-2 rounded-lg border px-3 py-2 text-left text-sm transition-colors ${
          failed
            ? 'border-err/40 bg-err/5'
            : running
              ? 'border-accent/40 bg-panel2'
              : 'border-edge bg-panel2'
        } hover:bg-panel2/70`}
      >
        {running ? (
          <Loader2
            size="1.0000rem"
            className="animate-spin shrink-0 text-accent"
          />
        ) : failed ? (
          <X size="1.0000rem" className="shrink-0 text-err" />
        ) : (
          <Bot size="1.0000rem" className="shrink-0 text-accent" />
        )}
        <span className="min-w-0 flex-1 truncate text-sm text-fg">
          {verb} <code className="font-mono text-xs">{name}</code>
        </span>
        {!running && parsed !== null && (
          <span className="shrink-0 rounded bg-panel px-1.5 py-0.5 text-[0.7143rem] text-dim">
            {parsed.status ?? ''}
          </span>
        )}
        {open ? (
          <ChevronDown size="1.0000rem" className="shrink-0 text-dim" />
        ) : (
          <ChevronRight size="1.0000rem" className="shrink-0 text-dim" />
        )}
      </button>
      {open && (
        <div className="mt-1 space-y-1.5 overflow-hidden rounded-md border border-edge bg-panel/60 px-2.5 py-2">
          {running && (
            <div className="flex items-center gap-1.5 text-xs text-dim">
              <Loader2 size="0.8571rem" className="animate-spin" />
              {t('tool.running')}
            </div>
          )}
          {!running && failed && (
            <div className="whitespace-pre-wrap text-xs text-err">
              {tool.result}
            </div>
          )}
          {parsed !== null && (
            <>
              {parsed.description && (
                <div className="text-xs text-dim">{parsed.description}</div>
              )}
              {parsed.persisted_to && (
                <div className="truncate font-mono text-[0.7143rem] text-dim">
                  {parsed.persisted_to}
                </div>
              )}
              {parsed.hint && (
                <div className="text-[0.7143rem] text-dim">{parsed.hint}</div>
              )}
            </>
          )}
        </div>
      )}
    </div>
  );
}

// ToolSearchView renders tool_search as a standalone collapsible block:
// the header shows the query; expanding reveals the matching tools.
function ToolSearchView({ tool }: { tool: ToolView }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const args = parseArgs(tool);
  const query = args && typeof args.query === 'string' ? args.query : '';
  const parsed =
    tool.result !== undefined
      ? (() => {
          try {
            const v = JSON.parse(tool.result);
            if (v && typeof v === 'object' && Array.isArray(v.hits)) {
              return v as {
                hits: { name?: string; description?: string }[];
                selected?: string[];
              };
            }
          } catch {
            // not JSON
          }
          return null;
        })()
      : null;
  const running = tool.status === 'running';
  const failed = tool.status === 'error';
  const selectedSet = new Set(parsed?.selected ?? []);

  return (
    <div className="my-1.5">
      <button
        onClick={() => setOpen(!open)}
        className={`flex w-full items-center gap-2 rounded-lg border px-3 py-2 text-left text-sm transition-colors ${
          failed
            ? 'border-err/40 bg-err/5'
            : running
              ? 'border-accent/40 bg-panel2'
              : 'border-edge bg-panel2'
        } hover:bg-panel2/70`}
      >
        {running ? (
          <Loader2
            size="1.0000rem"
            className="animate-spin shrink-0 text-accent"
          />
        ) : failed ? (
          <X size="1.0000rem" className="shrink-0 text-err" />
        ) : (
          <Wrench size="1.0000rem" className="shrink-0 text-accent" />
        )}
        <span className="min-w-0 flex-1 truncate text-sm text-fg">
          {t('tool.searchedTools')}
          {query && <span className="text-dim">: {query}</span>}
        </span>
        {parsed !== null && !running && (
          <span className="shrink-0 rounded bg-panel px-1.5 py-0.5 font-mono text-[0.7143rem] text-dim tabular-nums">
            {parsed.hits.length} {t('tool.hits')}
            {selectedSet.size > 0
              ? ` · ${selectedSet.size} ${t('tool.selected')}`
              : ''}
          </span>
        )}
        {open ? (
          <ChevronDown size="1.0000rem" className="shrink-0 text-dim" />
        ) : (
          <ChevronRight size="1.0000rem" className="shrink-0 text-dim" />
        )}
      </button>
      {open && (
        <div className="mt-1 space-y-1.5 overflow-hidden rounded-md border border-edge bg-panel/60 px-2.5 py-2">
          {running && (
            <div className="flex items-center gap-1.5 text-xs text-dim">
              <Loader2 size="0.8571rem" className="animate-spin" />
              {t('tool.running')}
            </div>
          )}
          {!running && failed && (
            <div className="text-xs text-err">{tool.result}</div>
          )}
          {!running && parsed !== null && parsed.hits.length === 0 && (
            <div className="text-xs text-dim">{t('tool.noHits')}</div>
          )}
          {!running && parsed !== null && parsed.hits.length > 0 && (
            <div className="space-y-1.5">
              {parsed.hits.map((h) => {
                const isSelected = selectedSet.has(h.name ?? '');
                return (
                  <div key={h.name} className="flex items-start gap-2">
                    <code
                      className={`shrink-0 font-mono text-xs ${
                        isSelected ? 'text-ok' : 'text-fg'
                      }`}
                    >
                      {h.name}
                    </code>
                    {isSelected && (
                      <Check
                        size="0.8571rem"
                        className="mt-0.5 shrink-0 text-ok"
                      />
                    )}
                    {h.description && (
                      <span className="min-w-0 flex-1 text-xs text-dim">
                        {h.description}
                      </span>
                    )}
                  </div>
                );
              })}
            </div>
          )}
        </div>
      )}
    </div>
  );
}

function DirTree({ nodes, depth }: { nodes: DirNode[]; depth: number }) {
  return (
    <ul className={depth === 0 ? '' : 'ml-3 border-l border-edge pl-2'}>
      {nodes.map((node) => (
        <li key={node.name}>
          <div className="flex items-center gap-1.5 py-0.5">
            {node.type === 'dir' ? (
              <Folder size="0.9286rem" className="text-accent shrink-0" />
            ) : (
              <File size="0.8571rem" className="text-dim shrink-0" />
            )}
            <span className="min-w-0 truncate font-mono text-xs text-fg">
              {node.name}
            </span>
            {node.type === 'file' && fmtSize(node.size) && (
              <span className="ml-auto shrink-0 text-[0.7143rem] text-dim tabular-nums">
                {fmtSize(node.size)}
              </span>
            )}
          </div>
          {node.children.length > 0 && (
            <DirTree nodes={node.children} depth={depth + 1} />
          )}
        </li>
      ))}
    </ul>
  );
}

function resultSummary(
  tool: ToolView,
  t: (key: string) => string,
): { text: string; ok: boolean } | null {
  if (tool.result === undefined) return null;
  const execTail = (text: string): string => {
    const lines = text.trimEnd().split('\n');
    const last = lines[lines.length - 1]?.trim() ?? '';
    return last.length > 120 ? `${last.slice(0, 120)}…` : last;
  };
  const exec = execResult(tool.result);
  if (exec && typeof exec.exit_code === 'number') {
    if (exec.exit_code === 0) {
      const tail = execTail(exec.stdout ?? '');
      return tail
        ? { text: `└ ${t('tool.ok')} · ${tail}`, ok: true }
        : { text: `└ ${t('tool.ok')}`, ok: true };
    }
    const tail = execTail(exec.stderr ?? exec.stdout ?? '');
    return tail
      ? { text: `└ ${t('tool.exit')} ${exec.exit_code} · ${tail}`, ok: false }
      : { text: `└ ${t('tool.exit')} ${exec.exit_code}`, ok: false };
  }
  if (tool.status === 'error') return { text: '└ failed', ok: false };
  return null;
}

function DiffBlock({ patch }: { patch: string }) {
  const lines = patch.split('\n');
  return (
    <pre className="max-h-80 overflow-y-auto overflow-x-auto whitespace-pre-wrap break-all rounded-lg border border-edge bg-panel/60 p-2 font-mono text-xs">
      {lines.map((line, idx) => {
        let cls = 'text-dim';
        if (line.startsWith('+')) cls = 'text-ok';
        else if (line.startsWith('-')) cls = 'text-err';
        else if (line.startsWith('@@')) cls = 'text-accent';
        return (
          <div key={idx} className={cls}>
            {line}
          </div>
        );
      })}
    </pre>
  );
}

interface DiffHunk {
  oldStart: number;
  oldCount: number;
  newStart: number;
  newCount: number;
  lines: PatchLineDTO[];
}

// groupHunks splits rendered diff lines into contiguous hunks (line
// numbering restarts after each hunk), so each one renders a git-style
// "@@ -a,b +c,d @@" header.
function groupHunks(lines: PatchLineDTO[]): DiffHunk[] {
  const hunks: DiffHunk[] = [];
  let cur: DiffHunk | null = null;
  let prevOld = 0;
  let prevNew = 0;
  for (const line of lines) {
    const oldNum = line.old_num ?? 0;
    const newNum = line.new_num ?? 0;
    const oldBreak = oldNum > 0 && prevOld > 0 && oldNum !== prevOld + 1;
    const newBreak = newNum > 0 && prevNew > 0 && newNum !== prevNew + 1;
    if (!cur || oldBreak || newBreak) {
      cur = {
        oldStart: oldNum,
        oldCount: 0,
        newStart: newNum,
        newCount: 0,
        lines: [],
      };
      hunks.push(cur);
    }
    if (oldNum > 0) {
      if (!cur.oldStart) cur.oldStart = oldNum;
      cur.oldCount++;
    }
    if (newNum > 0) {
      if (!cur.newStart) cur.newStart = newNum;
      cur.newCount++;
    }
    cur.lines.push(line);
    prevOld = oldNum;
    prevNew = newNum;
  }
  return hunks;
}

function hunkHeader(h: DiffHunk): string {
  const fmt = (start: number, count: number) =>
    count === 1 ? String(start) : `${start},${count}`;
  return `@@ -${fmt(h.oldStart, h.oldCount)} +${fmt(h.newStart, h.newCount)} @@`;
}

function GitDiffLine({ line }: { line: PatchLineDTO }) {
  const oldCol = (line.old_num ?? 0) > 0 ? String(line.old_num) : '';
  const newCol = (line.new_num ?? 0) > 0 ? String(line.new_num) : '';
  const marker = line.kind === 'add' ? '+' : line.kind === 'delete' ? '-' : ' ';
  const isAdd = line.kind === 'add';
  const isDel = line.kind === 'delete';
  const numBg = isAdd ? 'bg-ok/15' : isDel ? 'bg-err/15' : 'bg-panel2/50';
  const lineBg = isAdd ? 'bg-ok/10' : isDel ? 'bg-err/10' : '';
  const markerCls = isAdd ? 'text-ok' : isDel ? 'text-err' : 'text-dim';
  return (
    <div
      className={`grid grid-cols-[4rem_4rem_minmax(0,1fr)] font-mono text-xs leading-5 ${lineBg}`}
    >
      <div
        className={`select-none px-2 text-right text-dim tabular-nums ${numBg}`}
      >
        {oldCol}
      </div>
      <div
        className={`select-none px-2 text-right text-dim tabular-nums ${numBg}`}
      >
        {newCol}
      </div>
      <div className="whitespace-pre px-2 text-fg">
        <span className={`select-none ${markerCls}`}>{marker}</span>
        {line.text}
      </div>
    </div>
  );
}

function FileHeader({
  file,
  open,
  onToggle,
  collapsible,
}: {
  file: PatchFileDTO;
  open: boolean;
  onToggle: () => void;
  collapsible: boolean;
}) {
  return (
    <div className="sticky top-0 z-10 flex items-center gap-2 border-b border-edge bg-panel px-2 py-1.5">
      {collapsible && (
        <button
          onClick={onToggle}
          className="text-dim hover:text-fg"
          title={open ? 'Collapse' : 'Expand'}
        >
          {open ? (
            <ChevronDown size="0.9286rem" />
          ) : (
            <ChevronRight size="0.9286rem" />
          )}
        </button>
      )}
      <span className="min-w-0 truncate font-mono text-xs text-fg">
        {file.path}
      </span>
      <span className="flex-1" />
      <span className="text-[0.7143rem] text-ok tabular-nums">
        +{file.added}
      </span>
      <span className="text-[0.7143rem] text-err tabular-nums">
        −{file.removed}
      </span>
    </div>
  );
}

function FileDiff({
  file,
  collapsible,
}: {
  file: PatchFileDTO;
  collapsible: boolean;
}) {
  const [open, setOpen] = useState(true);
  const hunks = groupHunks(file.lines);
  return (
    <div className="border-b border-edge last:border-b-0">
      <FileHeader
        file={file}
        open={open}
        onToggle={() => setOpen((v) => !v)}
        collapsible={collapsible}
      />
      {(!collapsible || open) &&
        hunks.map((h, i) => (
          <div key={i}>
            <div className="select-none bg-panel2/60 px-3 py-0.5 text-center font-mono text-[0.7143rem] text-accent">
              {hunkHeader(h)}
            </div>
            {h.lines.map((line, j) => (
              <GitDiffLine key={j} line={line} />
            ))}
          </div>
        ))}
    </div>
  );
}

function GitDiffView({
  files,
  collapsible = true,
  maxHeight = 'max-h-80',
}: {
  files: PatchFileDTO[];
  collapsible?: boolean;
  maxHeight?: string;
}) {
  return (
    <div
      className={`${maxHeight} overflow-y-auto rounded-lg border border-edge bg-panel/60`}
    >
      {files.map((f) => (
        <FileDiff key={f.path} file={f} collapsible={collapsible} />
      ))}
    </div>
  );
}

// usePatchFiles renders the patch argument of apply_patch /
// skill_modify as a git diff against the current file content,
// computed server-side. Falls back to the raw colored patch while the
// preview loads or when it fails.
function usePatchFiles(tool: ToolView): {
  files: PatchFileDTO[] | null;
  failed: boolean;
  patch: string;
} {
  const args = parseArgs(tool);
  const patch = args && typeof args.patch === 'string' ? args.patch : tool.args;
  const name = args && typeof args.name === 'string' ? args.name : '';
  const scope = args && typeof args.scope === 'string' ? args.scope : '';
  const [files, setFiles] = useState<PatchFileDTO[] | null>(null);
  const [failed, setFailed] = useState(false);

  useEffect(() => {
    let cancelled = false;
    setFiles(null);
    setFailed(false);
    const req =
      tool.name === 'apply_patch'
        ? api.renderPatch(patch)
        : api.renderSkillPatch(name, scope, patch);
    void req
      .then((f) => {
        if (!cancelled) setFiles(f);
      })
      .catch(() => {
        if (!cancelled) setFailed(true);
      });
    return () => {
      cancelled = true;
    };
  }, [tool.name, patch, name, scope]);

  return { files, failed, patch };
}

// ApplyPatchView renders the apply_patch diff directly in the chat
// stream in full: the git diff is always visible in a ~10-line
// scrollable viewport, with a lightweight status line only while
// running or on failure.
export const ApplyPatchView = memo(function ApplyPatchView({
  tool,
}: {
  tool: ToolView;
}) {
  const { t } = useTranslation();
  const running = tool.status === 'running';
  const { files, failed, patch } = usePatchFiles(tool);
  const errored = tool.status === 'error';
  const resultFiles =
    tool.result !== undefined
      ? (() => {
          try {
            const v = JSON.parse(tool.result);
            if (v && typeof v === 'object' && Array.isArray(v.files)) {
              return v.files as {
                path?: string;
                action?: string;
              }[];
            }
          } catch {
            // not JSON
          }
          return null;
        })()
      : null;
  return (
    <div className="my-1.5 space-y-1">
      {running && (
        <div className="flex items-center gap-1.5 px-2.5 py-2 text-xs text-dim">
          <Loader2 size="0.8571rem" className="animate-spin" />
          {t('tool.running')}
        </div>
      )}
      {failed || files === null ? (
        <DiffBlock patch={patch} />
      ) : (
        <GitDiffView
          files={files}
          collapsible={false}
          maxHeight="max-h-[12.5rem]"
        />
      )}
      {!running && resultFiles !== null && resultFiles.length > 0 && (
        <div className="space-y-0.5 px-2.5 py-1 text-xs">
          {resultFiles.map((f, i) => (
            <div key={i} className="flex items-center gap-1.5 font-mono">
              <Check size="0.8571rem" className="shrink-0 text-ok" />
              <span className="min-w-0 truncate text-fg">{f.path ?? ''}</span>
              {f.action && (
                <span className="shrink-0 rounded bg-panel px-1 py-0.5 text-[0.7143rem] text-dim">
                  {f.action}
                </span>
              )}
            </div>
          ))}
        </div>
      )}
      {tool.result !== undefined && (
        <div
          className={`whitespace-pre-wrap break-all px-2.5 py-1 font-mono text-xs ${
            tool.status === 'error' ? 'text-err' : 'text-ok'
          }`}
        >
          {tool.result}
        </div>
      )}
    </div>
  );
});

function ArgsBlock({ tool }: { tool: ToolView }) {
  const { t } = useTranslation();
  if (tool.name === 'list_dir') {
    const args = parseArgs(tool);
    if (args) {
      const path = typeof args.path === 'string' ? args.path : '.';
      const flags: string[] = [];
      if (args.recursive) flags.push('recursive');
      if (args.include_hidden) flags.push('hidden');
      if (typeof args.max_depth === 'number') {
        flags.push(`depth ${args.max_depth}`);
      }
      return (
        <div className="flex items-center gap-2 text-xs">
          <Folder size="0.9286rem" className="text-accent shrink-0" />
          <code className="font-mono">{path}</code>
          {flags.length > 0 && (
            <span className="rounded border border-edge bg-panel px-1.5 py-0.5 text-[0.7143rem] text-dim">
              {flags.join(' · ')}
            </span>
          )}
        </div>
      );
    }
  }
  const pretty = (() => {
    const args = parseArgs(tool);
    return args ? JSON.stringify(args, null, 2) : tool.args;
  })();
  return (
    <pre className="text-xs overflow-x-auto whitespace-pre-wrap break-all font-mono text-dim max-h-48 overflow-y-auto">
      {pretty}
    </pre>
  );
}

function ResultBlock({ tool }: { tool: ToolView }) {
  const { t } = useTranslation();
  if (tool.result === undefined) return null;
  if (tool.name === 'list_dir') {
    const dir = parseDirResult(tool.result);
    if (dir && Array.isArray(dir.entries)) {
      const nodes = buildDirTree(dir.entries);
      return (
        <div className="space-y-1">
          <div className="flex items-center gap-1.5 text-xs">
            <Folder size="0.9286rem" className="text-accent shrink-0" />
            <code className="font-mono">{dir.path || '.'}</code>
            {dir.truncated && (
              <span className="text-[0.7143rem] text-warn">
                {t('tool.truncated')}
              </span>
            )}
          </div>
          <div className="max-h-64 overflow-y-auto rounded-md border border-edge bg-panel/60 px-2 py-1">
            <DirTree nodes={nodes} depth={0} />
          </div>
        </div>
      );
    }
  }
  // Results that turn out to be git diffs (e.g. read_file on a .diff
  // artifact) render as diff cards instead of raw text. The result may
  // be a JSON envelope {file_path, content, ...}; unwrap it first.
  const candidate = (() => {
    try {
      const v = JSON.parse(tool.result);
      if (v && typeof v === 'object' && typeof v.content === 'string') {
        return v.content;
      }
    } catch {
      // not JSON
    }
    // Corrupt result JSON (historical archives): recover the content
    // value so diff detection still runs.
    return recoverJsonContent(tool.result) ?? tool.result;
  })();
  const diffFiles = looksLikeUnifiedDiff(candidate)
    ? parseUnifiedDiff(candidate)
    : null;
  if (diffFiles) {
    return <GitDiffView files={diffFiles} maxHeight="max-h-64" />;
  }
  return (
    <pre
      className={`text-xs whitespace-pre-wrap break-all font-mono max-h-64 overflow-y-auto ${
        tool.status === 'error' ? 'text-err' : 'text-dim'
      }`}
    >
      {tool.result}
    </pre>
  );
}

export const ToolCard = memo(function ToolCard({ tool }: { tool: ToolView }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const [copied, setCopied] = useState(false);
  const running = tool.status === 'running';
  const failed = tool.status === 'error';
  // Live tools stay expanded while running so the progress is visible.
  const liveTools = [
    'web_fetch',
    'generate_image',
    'generate_video',
    'apply_patch',
  ];
  useEffect(() => {
    if (running && liveTools.includes(tool.name)) setOpen(true);
  }, [running, tool.name]);

  // exec / read render their own standalone collapsible views without
  // the generic tool card wrapper.
  if (tool.name === 'exec_command' || tool.name === 'exec_session') {
    return <ExecView tool={tool} />;
  }
  if (tool.name === 'read_file') {
    return <ReadView tool={tool} />;
  }
  if (tool.name === 'write_file') {
    return <WriteView tool={tool} />;
  }
  if (tool.name === 'ask_user') {
    return <AskUserView tool={tool} />;
  }
  if (tool.name === 'skill_search') {
    return <SkillSearchView tool={tool} />;
  }
  if (tool.name === 'delegate') {
    return <DelegateView tool={tool} />;
  }
  if (tool.name === 'delegation_status') {
    return <DelegationStatusView tool={tool} />;
  }
  if (tool.name === 'delegation_targets') {
    return <DelegationTargetsView tool={tool} />;
  }
  if (mcpNameParts(tool.name) !== null) {
    return <McpToolView tool={tool} />;
  }
  if (tool.name === 'grep') {
    return <GrepView tool={tool} />;
  }
  if (tool.name === 'glob') {
    return <GlobView tool={tool} />;
  }
  if (tool.name === 'request_permissions') {
    return <PermissionsView tool={tool} />;
  }
  if (tool.name === 'skill_read') {
    return <SkillReadView tool={tool} />;
  }
  if (tool.name === 'web_fetch') {
    return <WebFetchView tool={tool} />;
  }
  if (tool.name === 'generate_image' || tool.name === 'generate_video') {
    return <GenerateView tool={tool} />;
  }
  if (
    tool.name === 'skill_install' ||
    tool.name === 'skill_create' ||
    tool.name === 'skill_modify'
  ) {
    return <SkillManageView tool={tool} />;
  }
  if (
    tool.name === 'create_agent' ||
    tool.name === 'update_agent' ||
    tool.name === 'unregister_agent'
  ) {
    return <AgentView tool={tool} />;
  }
  if (tool.name === 'tool_search') {
    return <ToolSearchView tool={tool} />;
  }
  const summary = summaryOf(tool);
  const summaryLine = resultSummary(tool, t);

  const copyResult = async () => {
    if (tool.result === undefined) return;
    try {
      await navigator.clipboard.writeText(tool.result);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // clipboard unavailable
    }
  };

  return (
    <div
      className={`rounded-lg border overflow-hidden my-1.5 ${
        running
          ? 'border-accent/40 bg-panel2'
          : failed
            ? 'border-err/40 bg-err/5'
            : 'border-edge bg-panel2'
      }`}
    >
      <button
        onClick={() => setOpen(!open)}
        className="w-full flex items-center gap-2 px-3 py-2 text-left text-sm hover:bg-panel2/70"
      >
        {running ? (
          <Loader2
            size="1.0000rem"
            className="text-accent animate-spin shrink-0"
          />
        ) : failed ? (
          <X size="1.0000rem" className="text-err shrink-0" />
        ) : (
          <Check size="1.0000rem" className="text-ok shrink-0" />
        )}
        {summary ? (
          <span className="truncate min-w-0">
            <span className="text-dim">{t(`tool.${summary.verb}`)}</span>{' '}
            <code className="font-mono text-xs break-all">
              {summary.rest || tool.name}
            </code>
          </span>
        ) : (
          <code className="font-mono text-xs truncate">{tool.name}</code>
        )}
        <span className="flex-1" />
        <span className="text-xs text-dim shrink-0">
          {t(`tool.${tool.status}`)}
        </span>
        {open ? (
          <ChevronDown size="1.0000rem" className="text-dim shrink-0" />
        ) : (
          <ChevronRight size="1.0000rem" className="text-dim shrink-0" />
        )}
      </button>
      {!open && summaryLine && (
        <div
          className={`px-3 pb-2 text-xs font-mono ${
            summaryLine.ok ? 'text-ok' : 'text-err'
          }`}
        >
          {summaryLine.text}
        </div>
      )}
      {open && (
        <div className="border-t border-edge px-3 py-2 space-y-2">
          <div className="text-xs text-dim">{t('tool.arguments')}:</div>
          <ArgsBlock tool={tool} />
          {tool.result !== undefined && (
            <>
              <div className="flex items-center justify-between pt-1">
                <div className="text-xs text-dim">{t('tool.result')}:</div>
                <div className="flex items-center gap-2 text-[0.7143rem] text-dim">
                  <span className="tabular-nums">
                    {tool.result.split('\n').length} {t('tool.lines')}
                  </span>
                  <button
                    onClick={() => void copyResult()}
                    className="flex items-center gap-1 rounded border border-edge px-1.5 py-0.5 text-dim hover:text-fg"
                    aria-label={t('tool.copyResult')}
                  >
                    {copied ? (
                      <Check size="0.7857rem" />
                    ) : (
                      <ClipboardList size="0.7857rem" />
                    )}
                    {t('tool.copyResult')}
                  </button>
                </div>
              </div>
              <ResultBlock tool={tool} />
            </>
          )}
        </div>
      )}
    </div>
  );
});
