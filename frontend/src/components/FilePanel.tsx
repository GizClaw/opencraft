import { useCallback, useEffect, useState } from "react";
import {
  ArrowLeft,
  ChevronDown,
  ChevronRight,
  File,
  FileCode,
  FileDiff as FileDiffIcon,
  Folder,
  FolderOpen,
  RefreshCw,
} from "lucide-react";
import { useTranslation } from "react-i18next";
import { api } from "../lib/api";
import { useStore } from "../lib/store";
import type { FileNode } from "../lib/types";

const codeExt =
  /\.(go|ts|tsx|js|jsx|rs|py|json|yaml|yml|toml|md|sh|sql|c|h|java|kt|swift|html|css)$/i;

function TreeNode({
  node,
  depth,
  onOpen,
}: {
  node: FileNode;
  depth: number;
  onOpen: (n: FileNode) => void;
}) {
  const [expanded, setExpanded] = useState(false);
  const [children, setChildren] = useState<FileNode[]>([]);
  const [loaded, setLoaded] = useState(false);

  const load = useCallback(async () => {
    if (loaded) return;
    try {
      setChildren(await api.listDir(node.path));
    } finally {
      setLoaded(true);
    }
  }, [loaded, node.path]);

  const toggle = () => {
    if (node.is_dir) {
      if (!expanded) void load();
      setExpanded(!expanded);
    } else {
      onOpen(node);
    }
  };

  const Icon = node.is_dir
    ? expanded
      ? FolderOpen
      : Folder
    : codeExt.test(node.name)
      ? FileCode
      : File;

  return (
    <div>
      <button
        onClick={toggle}
        onDoubleClick={() => {
          if (!node.is_dir) void api.openPath(node.path);
        }}
        className="w-full flex items-center gap-1.5 px-2 py-1 rounded text-sm hover:bg-panel2 text-left"
        style={{ paddingLeft: 6 + depth * 14 }}
        title={node.path}
      >
        {node.is_dir ? (
          expanded ? (
            <ChevronDown size={13} className="text-dim shrink-0" />
          ) : (
            <ChevronRight size={13} className="text-dim shrink-0" />
          )
        ) : (
          <span className="w-[13px] shrink-0" />
        )}
        <Icon size={14} className={node.is_dir ? "text-accent" : "text-dim"} />
        <span className="truncate">{node.name}</span>
      </button>
      {expanded &&
        children.map((child) => (
          <TreeNode
            key={child.path}
            node={child}
            depth={depth + 1}
            onOpen={onOpen}
          />
        ))}
    </div>
  );
}

function DiffBlock({ text }: { text: string }) {
  const lines = text.split("\n");
  return (
    <pre className="text-xs overflow-x-auto whitespace-pre-wrap break-all font-mono max-h-full overflow-y-auto">
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

export function FilePanel() {
  const workspace = useStore((s) => s.workspace);
  const [tab, setTab] = useState<"tree" | "view">("tree");
  const [root, setRoot] = useState<FileNode[]>([]);
  const [current, setCurrent] = useState<FileNode | null>(null);
  const [content, setContent] = useState("");
  const [diff, setDiff] = useState("");
  const [showDiff, setShowDiff] = useState(false);
  const [loading, setLoading] = useState(false);
  const [viewError, setViewError] = useState("");
  const { t } = useTranslation();

  const load = useCallback(async () => {
    try {
      setRoot(await api.listDir(workspace));
    } catch {
      setRoot([]);
    }
  }, [workspace]);

  useEffect(() => {
    void load();
  }, [load]);

  const openView = async (node: FileNode) => {
    setTab("view");
    setCurrent(node);
    setShowDiff(false);
    setLoading(true);
    setViewError("");
    try {
      const [c, d] = await Promise.all([
        api.readFile(node.path),
        api.fileDiff(node.path),
      ]);
      setContent(c);
      setDiff(d);
    } catch (err) {
      setViewError(String(err));
      setContent("");
      setDiff("");
    } finally {
      setLoading(false);
    }
  };

  return (
    <aside className="w-80 shrink-0 border-l border-edge bg-panel flex flex-col min-h-0">
      <div className="px-3 py-2 border-b border-edge flex items-center gap-1">
        <button
          onClick={() => setTab("tree")}
          className={`rounded px-2 py-0.5 text-xs ${
            tab === "tree"
              ? "bg-accent text-white"
              : "text-dim hover:text-fg"
          }`}
        >
          {t("files.tree")}
        </button>
        <button
          onClick={() => current && setTab("view")}
          disabled={!current}
          className={`rounded px-2 py-0.5 text-xs disabled:opacity-40 ${
            tab === "view"
              ? "bg-accent text-white"
              : "text-dim hover:text-fg"
          }`}
        >
          {t("files.view")}
        </button>
        <span className="flex-1" />
        {tab === "tree" && (
          <button onClick={() => void load()} className="text-dim hover:text-fg">
            <RefreshCw size={12} />
          </button>
        )}
      </div>

      {tab === "tree" ? (
        <div className="flex-1 overflow-y-auto py-2">
          {root.map((node) => (
            <TreeNode
              key={node.path}
              node={node}
              depth={0}
              onOpen={(n) => void openView(n)}
            />
          ))}
        </div>
      ) : (
        <div className="flex-1 flex flex-col min-h-0">
          <div className="px-3 py-2 border-b border-edge flex items-center gap-2">
            <button
              onClick={() => setTab("tree")}
              className="text-dim hover:text-fg"
              title={t("files.tree")}
            >
              <ArrowLeft size={14} />
            </button>
            <span
              className="text-xs text-dim truncate flex-1"
              title={current?.path}
            >
              {current?.path.replace(workspace + "/", "")}
            </span>
            <button
              onClick={() => setShowDiff(!showDiff)}
              disabled={!diff}
              className={`flex items-center gap-1 rounded px-2 py-0.5 text-xs disabled:opacity-40 ${
                showDiff
                  ? "bg-accent text-white"
                  : "text-dim hover:text-fg border border-edge"
              }`}
            >
              <FileDiffIcon size={12} />
              {t("files.diff")}
            </button>
          </div>
          <div className="flex-1 overflow-hidden">
            {loading ? (
              <div className="h-full grid place-items-center text-dim text-xs">
                …
              </div>
            ) : viewError ? (
              <div className="p-3 text-xs text-err whitespace-pre-wrap break-all">
                {viewError}
              </div>
            ) : showDiff ? (
              diff ? (
                <div className="h-full overflow-y-auto p-3">
                  <DiffBlock text={diff} />
                </div>
              ) : (
                <div className="h-full grid place-items-center text-dim text-xs">
                  {t("files.noDiff")}
                </div>
              )
            ) : (
              <pre className="h-full overflow-y-auto p-3 text-xs whitespace-pre-wrap break-all font-mono">
                {content}
              </pre>
            )}
          </div>
        </div>
      )}
    </aside>
  );
}
