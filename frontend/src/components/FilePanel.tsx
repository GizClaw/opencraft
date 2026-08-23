import { useCallback, useEffect, useState } from "react";
import {
  ChevronDown,
  ChevronRight,
  File,
  FileCode,
  Folder,
  FolderOpen,
  RefreshCw,
} from "lucide-react";
import { api } from "../lib/api";
import { useStore } from "../lib/store";
import type { FileNode } from "../lib/types";

const codeExt = /\.(go|ts|tsx|js|jsx|rs|py|json|yaml|yml|toml|md|sh|sql|c|h|java|kt|swift|html|css)$/i;

function TreeNode({ node, depth }: { node: FileNode; depth: number }) {
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
    if (!expanded) void load();
    setExpanded(!expanded);
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
          <TreeNode key={child.path} node={child} depth={depth + 1} />
        ))}
    </div>
  );
}

export function FilePanel() {
  const workspace = useStore((s) => s.workspace);
  const [root, setRoot] = useState<FileNode[]>([]);

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

  return (
    <aside className="w-72 shrink-0 border-l border-edge bg-panel flex flex-col min-h-0">
      <div className="px-3 py-2 border-b border-edge flex items-center justify-between">
        <span className="text-xs uppercase tracking-wider text-dim">工作区</span>
        <button onClick={() => void load()} className="text-dim hover:text-fg">
          <RefreshCw size={12} />
        </button>
      </div>
      <div className="flex-1 overflow-y-auto py-2">
        {root.map((node) => (
          <TreeNode key={node.path} node={node} depth={0} />
        ))}
      </div>
    </aside>
  );
}
