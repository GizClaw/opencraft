import { memo, useEffect, useState } from 'react';
import {
  Check,
  ChevronDown,
  ChevronRight,
  ClipboardList,
  Loader2,
  X,
} from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { api } from '../lib/api';
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
  if (tool.name === 'read_file') {
    try {
      const v = JSON.parse(tool.result);
      if (v && typeof v.file_path === 'string') {
        return { text: `└ ${v.file_path}`, ok: true };
      }
    } catch {
      // fall through
    }
  }
  if (tool.name === 'tool_search') {
    try {
      const v = JSON.parse(tool.result);
      if (v && typeof v === 'object' && Array.isArray(v.hits)) {
        const hits = v.hits.length;
        const selected = Array.isArray(v.selected) ? v.selected.length : 0;
        return {
          text: `└ ${hits} ${t('tool.hits')}${
            selected > 0 ? ` · ${selected} ${t('tool.selected')}` : ''
          }`,
          ok: true,
        };
      }
    } catch {
      // fall through
    }
  }
  if (tool.status === 'error') return { text: '└ failed', ok: false };
  return null;
}

interface PlanStep {
  step?: string;
  status?: string;
}

interface PlanArgs {
  explanation?: string;
  plan: PlanStep[];
}

// planArgs extracts the update_plan snapshot from the tool arguments.
function planArgs(tool: ToolView): PlanArgs | null {
  const args = parseArgs(tool);
  if (!args || !Array.isArray(args.plan)) return null;
  return {
    explanation:
      typeof args.explanation === 'string' ? args.explanation : undefined,
    plan: args.plan as PlanStep[],
  };
}

// PlanTodoCard renders update_plan as a to-do list instead of a generic
// tool item: explanation plus one row per step with a status marker.
function PlanTodoCard({
  tool,
  plan,
  t,
}: {
  tool: ToolView;
  plan: PlanArgs;
  t: (key: string) => string;
}) {
  const [open, setOpen] = useState(() => {
    // Fully completed plans collapse into the header; active plans stay
    // expanded so the checklist is visible while work is running.
    return !plan.plan.every((s) => s.status === 'completed');
  });
  const running = tool.status === 'running';
  const failed = tool.status === 'error';
  const done = plan.plan.filter((s) => s.status === 'completed').length;
  return (
    <div
      className={`rounded-lg border overflow-hidden my-1.5 ${
        failed ? 'border-err/40 bg-err/5' : 'border-edge bg-panel2'
      }`}
    >
      <button
        onClick={() => setOpen(!open)}
        className="w-full flex items-center gap-2 px-3 py-1.5 text-xs border-b border-edge hover:bg-panel2/70"
      >
        {running ? (
          <Loader2 size={13} className="text-accent animate-spin shrink-0" />
        ) : failed ? (
          <X size={13} className="text-err shrink-0" />
        ) : (
          <ClipboardList size={13} className="text-ok shrink-0" />
        )}
        <span className="text-dim">{t('tool.updatePlan')}</span>
        <span className="text-dim">
          {done}/{plan.plan.length}
        </span>
        <span className="flex-1" />
        <span className="text-dim">{t(`tool.${tool.status}`)}</span>
        {open ? (
          <ChevronDown size={14} className="text-dim shrink-0" />
        ) : (
          <ChevronRight size={14} className="text-dim shrink-0" />
        )}
      </button>
      {open && (
        <div className="px-3 py-2 space-y-1.5">
          {plan.explanation && (
            <div className="text-xs text-dim whitespace-pre-wrap">
              {plan.explanation}
            </div>
          )}
          {plan.plan.map((item, idx) => {
            const status = item.status ?? 'pending';
            const completed = status === 'completed';
            const inProgress = status === 'in_progress';
            return (
              <div key={idx} className="flex items-start gap-2 text-xs">
                {inProgress ? (
                  <Loader2
                    size={12}
                    className="text-accent animate-spin shrink-0 mt-0.5"
                  />
                ) : completed ? (
                  <Check size={12} className="text-ok shrink-0 mt-0.5" />
                ) : (
                  <span className="h-3 w-3 rounded-full border border-dim shrink-0 mt-0.5" />
                )}
                <span
                  className={`min-w-0 ${
                    completed ? 'text-dim line-through' : 'text-fg'
                  }`}
                >
                  {item.step}
                </span>
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}

function DiffBlock({ patch }: { patch: string }) {
  const lines = patch.split('\n');
  return (
    <pre className="text-xs overflow-x-auto whitespace-pre-wrap break-all font-mono max-h-80 overflow-y-auto">
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
}: {
  file: PatchFileDTO;
  open: boolean;
  onToggle: () => void;
}) {
  return (
    <div className="sticky top-0 z-10 flex items-center gap-2 border-b border-edge bg-panel px-2 py-1.5">
      <button
        onClick={onToggle}
        className="text-dim hover:text-fg"
        title={open ? 'Collapse' : 'Expand'}
      >
        {open ? <ChevronDown size={13} /> : <ChevronRight size={13} />}
      </button>
      <span className="min-w-0 truncate font-mono text-xs text-fg">
        {file.path}
      </span>
      <span className="flex-1" />
      <span className="text-[10px] text-ok tabular-nums">+{file.added}</span>
      <span className="text-[10px] text-err tabular-nums">−{file.removed}</span>
    </div>
  );
}

function FileDiff({ file }: { file: PatchFileDTO }) {
  const [open, setOpen] = useState(true);
  const hunks = groupHunks(file.lines);
  return (
    <div className="border-b border-edge last:border-b-0">
      <FileHeader file={file} open={open} onToggle={() => setOpen((v) => !v)} />
      {open &&
        hunks.map((h, i) => (
          <div key={i}>
            <div className="select-none bg-panel2/60 px-3 py-0.5 text-center font-mono text-[10px] text-accent">
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

function GitDiffView({ files }: { files: PatchFileDTO[] }) {
  return (
    <div className="max-h-80 overflow-y-auto rounded-lg border border-edge bg-panel/60">
      {files.map((f) => (
        <FileDiff key={f.path} file={f} />
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

function PatchPreview({ tool }: { tool: ToolView }) {
  const { files, failed, patch } = usePatchFiles(tool);
  if (failed || files === null) return <DiffBlock patch={patch} />;
  return <GitDiffView files={files} />;
}

// ApplyPatchView renders the apply_patch diff directly in the chat
// stream, without the generic tool card wrapper around it.
export const ApplyPatchView = memo(function ApplyPatchView({
  tool,
}: {
  tool: ToolView;
}) {
  const { t } = useTranslation();
  const { files, failed, patch } = usePatchFiles(tool);
  return (
    <div className="my-1.5 space-y-1">
      {failed || files === null ? (
        <DiffBlock patch={patch} />
      ) : (
        <GitDiffView files={files} />
      )}
      {tool.result !== undefined && (
        <div
          className={`whitespace-pre-wrap break-all font-mono text-xs ${
            tool.status === 'error' ? 'text-err' : 'text-ok'
          }`}
        >
          {tool.result}
        </div>
      )}
      {tool.status === 'running' && (
        <div className="flex items-center gap-1.5 text-xs text-dim">
          <Loader2 size={12} className="animate-spin" />
          {t('tool.running')}
        </div>
      )}
    </div>
  );
});

function ArgsBlock({ tool }: { tool: ToolView }) {
  const { t } = useTranslation();
  if (tool.name === 'apply_patch') {
    return <PatchPreview tool={tool} />;
  }
  if (tool.name === 'write_file') {
    const args = parseArgs(tool);
    const path =
      args && typeof args.file_path === 'string' ? args.file_path : '';
    const content =
      args && typeof args.content === 'string' ? args.content : '';
    const lines = content.split('\n').map((text, i) => ({
      kind: 'add' as const,
      old_num: 0,
      new_num: i + 1,
      text,
    }));
    return (
      <GitDiffView
        files={[
          {
            path,
            action: 'add',
            added: lines.length,
            removed: 0,
            lines,
          },
        ]}
      />
    );
  }
  if (tool.name === 'skill_modify') {
    const args = parseArgs(tool);
    if (args && typeof args.patch === 'string') {
      return <PatchPreview tool={tool} />;
    }
  }
  if (tool.name === 'tool_search') {
    const args = parseArgs(tool);
    if (args) {
      const query = typeof args.query === 'string' ? args.query : '';
      const select = Array.isArray(args.select)
        ? args.select.filter((s): s is string => typeof s === 'string')
        : [];
      return (
        <div className="space-y-1">
          <div className="text-xs">
            <span className="text-dim">{t('tool.query')}: </span>
            <code className="font-mono">{query}</code>
          </div>
          {typeof args.limit === 'number' && (
            <div className="text-xs">
              <span className="text-dim">{t('tool.limit')}: </span>
              <code className="font-mono">{args.limit}</code>
            </div>
          )}
          {select.length > 0 && (
            <div className="text-xs">
              <span className="text-dim">{t('tool.select')}: </span>
              <span className="font-mono text-ok">{select.join(', ')}</span>
            </div>
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
  if (tool.name === 'tool_search') {
    try {
      const v = JSON.parse(tool.result);
      if (v && typeof v === 'object' && Array.isArray(v.hits)) {
        const selected = Array.isArray(v.selected)
          ? new Set(v.selected as string[])
          : new Set<string>();
        const hits = v.hits as {
          name?: string;
          description?: string;
        }[];
        if (hits.length === 0) {
          return <div className="text-xs text-dim">{t('tool.noHits')}</div>;
        }
        return (
          <div className="space-y-1.5">
            {hits.map((h) => {
              const name = h.name ?? '';
              const isSelected = selected.has(name);
              return (
                <div key={name} className="flex items-start gap-2">
                  <code
                    className={`shrink-0 font-mono text-xs ${
                      isSelected ? 'text-ok' : 'text-fg'
                    }`}
                  >
                    {name}
                  </code>
                  {isSelected && (
                    <Check size={12} className="mt-0.5 shrink-0 text-ok" />
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
        );
      }
    } catch {
      // fall through to the generic JSON block
    }
  }
  const exec = execResult(tool.result);
  if (exec) {
    return (
      <div className="space-y-1">
        {typeof exec.stderr === 'string' && exec.stderr && (
          <pre className="text-xs text-err whitespace-pre-wrap break-all font-mono max-h-48 overflow-y-auto">
            {exec.stderr}
          </pre>
        )}
        {typeof exec.stdout === 'string' && exec.stdout && (
          <pre className="text-xs text-dim whitespace-pre-wrap break-all font-mono max-h-48 overflow-y-auto">
            {exec.stdout}
          </pre>
        )}
        <div
          className={`text-xs font-mono ${
            exec.exit_code === 0 ? 'text-ok' : 'text-err'
          }`}
        >
          {exec.exit_code === 0
            ? `└ ${t('tool.ok')}`
            : `└ ${t('tool.exit')} ${exec.exit_code}`}
        </div>
      </div>
    );
  }
  if (tool.name === 'read_file') {
    try {
      const v = JSON.parse(tool.result);
      if (v && typeof v.content === 'string') {
        return (
          <pre className="text-xs text-fg whitespace-pre-wrap break-all font-mono max-h-64 overflow-y-auto">
            {v.content}
          </pre>
        );
      }
    } catch {
      // fall through
    }
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
  const [open, setOpen] = useState(false);
  const [copied, setCopied] = useState(false);
  const { t } = useTranslation();
  // update_plan renders as a to-do list, not a generic tool item.
  if (tool.name === 'update_plan') {
    const plan = planArgs(tool);
    if (plan && plan.plan.length > 0) {
      return <PlanTodoCard tool={tool} plan={plan} t={t} />;
    }
  }
  const summary = summaryOf(tool);
  const summaryLine = resultSummary(tool, t);
  const running = tool.status === 'running';
  const failed = tool.status === 'error';
  // Live tools stay expanded while running so the progress is visible.
  const liveTools = [
    'exec_command',
    'exec_session',
    'web_fetch',
    'generate_image',
    'generate_video',
    'apply_patch',
  ];
  useEffect(() => {
    if (running && liveTools.includes(tool.name)) setOpen(true);
  }, [running, tool.name]);

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
          <Loader2 size={14} className="text-accent animate-spin shrink-0" />
        ) : failed ? (
          <X size={14} className="text-err shrink-0" />
        ) : (
          <Check size={14} className="text-ok shrink-0" />
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
          <ChevronDown size={14} className="text-dim shrink-0" />
        ) : (
          <ChevronRight size={14} className="text-dim shrink-0" />
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
                <div className="flex items-center gap-2 text-[10px] text-dim">
                  <span className="tabular-nums">
                    {tool.result.split('\n').length} {t('tool.lines')}
                  </span>
                  <button
                    onClick={() => void copyResult()}
                    className="flex items-center gap-1 rounded border border-edge px-1.5 py-0.5 text-dim hover:text-fg"
                    aria-label={t('tool.copyResult')}
                  >
                    {copied ? <Check size={11} /> : <ClipboardList size={11} />}
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
