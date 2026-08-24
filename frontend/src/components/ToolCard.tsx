import { useState } from "react";
import {
  Check,
  ChevronDown,
  ChevronRight,
  Loader2,
  X,
} from "lucide-react";
import { useTranslation } from "react-i18next";
import type { ToolView } from "../lib/store";

function parseArgs(tool: ToolView): Record<string, unknown> | null {
  try {
    const v = JSON.parse(tool.args);
    return v && typeof v === "object" ? v : null;
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
  const str = (v: unknown): string => (typeof v === "string" ? v : "");
  switch (tool.name) {
    case "exec_command":
    case "exec_session":
      return {
        verb: "ran",
        rest:
          str(args.command) ||
          (Array.isArray(args.argv) ? args.argv.join(" ") : ""),
      };
    case "read_file":
      return { verb: "read", rest: str(args.file_path) };
    case "write_file":
      return { verb: "wrote", rest: str(args.file_path) };
    case "list_dir":
      return { verb: "listed", rest: str(args.path) || "." };
    case "grep": {
      const parts = [str(args.pattern)];
      if (str(args.path)) parts.push(`in ${args.path}`);
      return { verb: "grep", rest: parts.join(" ") };
    }
    case "glob":
      return { verb: "glob", rest: str(args.pattern) };
    case "request_permissions":
      return {
        verb: "requestPermissions",
        rest: Array.isArray(args.permissions)
          ? String(args.permissions.length)
          : "",
      };
    case "update_plan":
      return { verb: "updatePlan", rest: "" };
    case "apply_patch":
      return { verb: "patch", rest: "" };
    case "skill_create":
      return { verb: "createdSkill", rest: str(args.name) };
    case "skill_modify":
      return { verb: "modifiedSkill", rest: str(args.name) };
    case "skill_install":
      return { verb: "installedSkill", rest: str(args.name) || str(args.repo) };
    case "web_fetch":
      return { verb: "fetched", rest: str(args.url) };
    case "ask_user":
      return { verb: "askUser", rest: str(args.question) };
    case "skill_search":
      return { verb: "searchedSkill", rest: str(args.query) };
    case "skill_read":
      return { verb: "readSkill", rest: str(args.name) };
    case "create_agent":
      return { verb: "createdAgent", rest: str(args.name) };
    case "unregister_agent":
      return { verb: "unregisteredAgent", rest: str(args.name) };
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
    if (v && typeof v === "object") return v;
  } catch {
    // fall through
  }
  return null;
}

function resultSummary(tool: ToolView): { text: string; ok: boolean } | null {
  if (tool.result === undefined) return null;
  const exec = execResult(tool.result);
  if (exec && typeof exec.exit_code === "number") {
    return exec.exit_code === 0
      ? { text: "└ ok", ok: true }
      : { text: `└ exit ${exec.exit_code}`, ok: false };
  }
  if (tool.name === "read_file") {
    try {
      const v = JSON.parse(tool.result);
      if (v && typeof v.file_path === "string") {
        return { text: `└ ${v.file_path}`, ok: true };
      }
    } catch {
      // fall through
    }
  }
  if (tool.status === "error") return { text: "└ failed", ok: false };
  return null;
}

function PlanBlock({ args }: { args: Record<string, unknown> }) {
  const plan = Array.isArray(args.plan)
    ? (args.plan as { step?: string; status?: string }[])
    : [];
  if (plan.length === 0) return null;
  return (
    <div className="space-y-1">
      {plan.map((item, idx) => {
        const status = item.status ?? "";
        const marker =
          status === "completed"
            ? "[x]"
            : status === "in_progress"
              ? "[~]"
              : "[ ]";
        const color =
          status === "completed"
            ? "text-ok"
            : status === "in_progress"
              ? "text-accent"
              : "text-dim";
        return (
          <div key={idx} className={`text-xs font-mono ${color}`}>
            <span className="mr-1">{marker}</span>
            {item.step}
            {status && <span className="text-dim"> ({status})</span>}
          </div>
        );
      })}
      {typeof args.explanation === "string" && args.explanation && (
        <div className="text-xs text-dim">Explanation: {args.explanation}</div>
      )}
    </div>
  );
}

function DiffBlock({ patch }: { patch: string }) {
  const lines = patch.split("\n");
  return (
    <pre className="text-xs overflow-x-auto whitespace-pre-wrap break-all font-mono max-h-80 overflow-y-auto">
      {lines.map((line, idx) => {
        let cls = "text-dim";
        if (line.startsWith("+")) cls = "text-ok";
        else if (line.startsWith("-")) cls = "text-err";
        else if (line.startsWith("@@")) cls = "text-accent";
        return (
          <div key={idx} className={cls}>
            {line}
          </div>
        );
      })}
    </pre>
  );
}

function ArgsBlock({ tool }: { tool: ToolView }) {
  const { t } = useTranslation();
  if (tool.name === "apply_patch") {
    const args = parseArgs(tool);
    const patch =
      args && typeof args.patch === "string" ? args.patch : tool.args;
    return <DiffBlock patch={patch} />;
  }
  if (tool.name === "update_plan") {
    const args = parseArgs(tool);
    if (args) return <PlanBlock args={args} />;
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
  const exec = execResult(tool.result);
  if (exec) {
    return (
      <div className="space-y-1">
        {typeof exec.stderr === "string" && exec.stderr && (
          <pre className="text-xs text-err whitespace-pre-wrap break-all font-mono max-h-48 overflow-y-auto">
            {exec.stderr}
          </pre>
        )}
        {typeof exec.stdout === "string" && exec.stdout && (
          <pre className="text-xs text-dim whitespace-pre-wrap break-all font-mono max-h-48 overflow-y-auto">
            {exec.stdout}
          </pre>
        )}
        <div
          className={`text-xs font-mono ${
            exec.exit_code === 0 ? "text-ok" : "text-err"
          }`}
        >
          {exec.exit_code === 0
            ? `└ ${t("tool.ok")}`
            : `└ ${t("tool.exit")} ${exec.exit_code}`}
        </div>
      </div>
    );
  }
  if (tool.name === "read_file") {
    try {
      const v = JSON.parse(tool.result);
      if (v && typeof v.content === "string") {
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
        tool.status === "error" ? "text-err" : "text-dim"
      }`}
    >
      {tool.result}
    </pre>
  );
}

export function ToolCard({ tool }: { tool: ToolView }) {
  const [open, setOpen] = useState(false);
  const { t } = useTranslation();
  const summary = summaryOf(tool);
  const summaryLine = resultSummary(tool);
  const running = tool.status === "running";
  const failed = tool.status === "error";

  return (
    <div
      className={`rounded-lg border overflow-hidden my-1.5 ${
        failed ? "border-err/40 bg-err/5" : "border-edge bg-panel2"
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
            <span className="text-dim">{t(`tool.${summary.verb}`)}</span>{" "}
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
            summaryLine.ok ? "text-ok" : "text-err"
          }`}
        >
          {summaryLine.text}
        </div>
      )}
      {open && (
        <div className="border-t border-edge px-3 py-2 space-y-2">
          <div className="text-xs text-dim">{t("tool.arguments")}:</div>
          <ArgsBlock tool={tool} />
          {tool.result !== undefined && (
            <>
              <div className="text-xs text-dim pt-1">{t("tool.result")}:</div>
              <ResultBlock tool={tool} />
            </>
          )}
        </div>
      )}
    </div>
  );
}
