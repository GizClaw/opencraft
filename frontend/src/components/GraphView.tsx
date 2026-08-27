import { useEffect, useMemo, useState } from 'react';
import type { CSSProperties } from 'react';
import {
  Background,
  BackgroundVariant,
  BaseEdge,
  Controls,
  Handle,
  MiniMap,
  Position,
  ReactFlow,
  ReactFlowProvider,
  applyNodeChanges,
  useReactFlow,
  type Edge as RFEdge,
  type EdgeProps,
  type Node as RFNode,
  type NodeChange,
  type NodeProps,
} from '@xyflow/react';
import {
  Bot,
  Braces,
  CircleDashed,
  Loader2,
  Save,
  Terminal,
  Workflow,
  Wrench,
  X,
} from 'lucide-react';
import '@xyflow/react/dist/style.css';
import { useTranslation } from 'react-i18next';
import { api } from '../lib/api';
import type { GraphDTO, GraphNodeDTO } from '../lib/types';

// ---- layout ---------------------------------------------------------

const NODE_W = 220;
const NODE_H = 64;
const COL_GAP = 90;
const ROW_GAP = 36;
const PAD = 40;
const END_W = 34;

type Pos = { x: number; y: number; layer: number; row: number };

// placeNodes assigns a layered left-to-right layout: the entry is the
// leftmost column and every edge moves one column right. BFS from the
// entry over forward edges keeps loop/back edges from inflating the
// layout; rows within each column are ordered by predecessor barycenter
// so edges cross less.
function placeNodes(graph: GraphDTO): Map<string, Pos> {
  const layer = new Map<string, number>();
  layer.set(graph.entry, 0);
  const queue = [graph.entry];
  for (let head = 0; head < queue.length; head++) {
    const id = queue[head];
    const l = layer.get(id) ?? 0;
    for (const e of graph.edges) {
      if (e.from !== id || e.to === '__end__') continue;
      if (!layer.has(e.to)) {
        layer.set(e.to, l + 1);
        queue.push(e.to);
      }
    }
  }
  let maxLayer = 0;
  for (const n of graph.nodes) {
    if (!layer.has(n.id)) layer.set(n.id, 0);
    maxLayer = Math.max(maxLayer, layer.get(n.id) ?? 0);
  }
  const hasEnd = graph.edges.some((e) => e.to === '__end__');
  if (hasEnd) maxLayer += 1;

  const cols = new Map<number, string[]>();
  for (const n of graph.nodes) {
    const l = layer.get(n.id) ?? 0;
    const col = cols.get(l) ?? [];
    col.push(n.id);
    cols.set(l, col);
  }
  const rows = new Map<string, number>();
  const predOf = new Map<string, string[]>();
  for (const e of graph.edges) {
    if (e.to === '__end__') continue;
    const arr = predOf.get(e.to) ?? [];
    arr.push(e.from);
    predOf.set(e.to, arr);
  }
  for (const l of [...cols.keys()].sort((a, b) => a - b)) {
    const scored = (cols.get(l) ?? []).map((id) => {
      const preds = (predOf.get(id) ?? []).filter((p) => rows.has(p));
      const score = preds.length
        ? preds.reduce((s, p) => s + (rows.get(p) ?? 0), 0) / preds.length
        : -1;
      return { id, score };
    });
    scored.sort((a, b) => a.score - b.score || a.id.localeCompare(b.id));
    scored.forEach((s, i) => rows.set(s.id, i));
  }
  const endRow = hasEnd ? (cols.get(maxLayer - 1) ?? []).length : 0;

  const pos = new Map<string, Pos>();
  for (const n of graph.nodes) {
    const l = layer.get(n.id) ?? 0;
    pos.set(n.id, {
      layer: l,
      row: rows.get(n.id) ?? 0,
      x: PAD + l * (NODE_W + COL_GAP),
      y: PAD + (rows.get(n.id) ?? 0) * (NODE_H + ROW_GAP),
    });
  }
  if (hasEnd) {
    pos.set('__end__', {
      layer: maxLayer,
      row: endRow,
      x: PAD + maxLayer * (NODE_W + COL_GAP) + (NODE_W - END_W) / 2,
      y: PAD + endRow * (NODE_H + ROW_GAP) + (NODE_H - END_W) / 2,
    });
  }
  return pos;
}

// edgePathCoords draws one edge between raw handle coordinates:
// forward = smooth bezier, side = rounded elbow on the right of a
// column, back = stacked loop below both nodes.
function edgePathCoords(
  x1: number,
  y1: number,
  x2: number,
  y2: number,
  kind: 'forward' | 'side' | 'back',
  loopIndex = 0,
): string {
  if (kind === 'forward') {
    const dx = Math.max(40, Math.abs(x2 - x1) / 2);
    return `M ${x1} ${y1} C ${x1 + dx} ${y1}, ${x2 - dx} ${y2}, ${x2} ${y2}`;
  }
  if (kind === 'side') {
    const bend = 18;
    const xOut = x1 + bend;
    const yMid = (y1 + y2) / 2;
    return [
      `M ${x1} ${y1}`,
      `H ${xOut}`,
      `V ${yMid}`,
      `Q ${xOut} ${yMid + bend} ${xOut - bend} ${yMid + bend}`,
      `H ${x2 + bend}`,
      `Q ${x2} ${yMid + bend} ${x2} ${yMid}`,
      `V ${y2}`,
      `H ${x2}`,
    ].join(' ');
  }
  const bend = 30;
  const xOut = x1 + bend * 2;
  const xIn = x2 + bend * 2;
  const yLoop = Math.max(y1, y2) + bend * 2 + loopIndex * 26;
  return [
    `M ${x1} ${y1}`,
    `L ${xOut} ${y1}`,
    `Q ${xOut + bend} ${y1} ${xOut + bend} ${y1 + bend}`,
    `L ${xOut + bend} ${yLoop}`,
    `Q ${xOut + bend} ${yLoop + bend} ${xOut} ${yLoop + bend}`,
    `L ${xIn} ${yLoop + bend}`,
    `Q ${xIn - bend} ${yLoop + bend} ${xIn - bend} ${yLoop}`,
    `L ${xIn - bend} ${y2 + bend}`,
    `Q ${xIn - bend} ${y2} ${xIn} ${y2}`,
    `L ${x2} ${y2}`,
  ].join(' ');
}

function edgeMid(
  x1: number,
  y1: number,
  x2: number,
  y2: number,
  kind: 'forward' | 'side' | 'back',
  loopIndex = 0,
): { x: number; y: number } {
  if (kind === 'back') {
    return {
      x: (x1 + x2) / 2,
      y: Math.max(y1, y2) + 60 + 30 + 16 + loopIndex * 26,
    };
  }
  if (kind === 'side') {
    return { x: x1 + 18 + 8, y: (y1 + y2) / 2 };
  }
  return { x: (x1 + x2) / 2, y: (y1 + y2) / 2 - 14 };
}

// ConditionPill renders one edge condition as a small mono pill.
function ConditionPill({
  condition,
  x,
  y,
}: {
  condition: string;
  x: number;
  y: number;
}) {
  const label =
    condition.length > 30 ? `${condition.slice(0, 29)}…` : condition;
  const w = label.length * 5.6 + 14;
  return (
    <g
      transform={`translate(${x}, ${y})`}
      className="pointer-events-none select-none"
    >
      <rect
        x={-w / 2}
        y={-8}
        width={w}
        height={16}
        rx={8}
        fill="var(--color-panel2)"
        stroke="var(--color-edge)"
      />
      <text
        x={0}
        y={3}
        fontSize={9}
        textAnchor="middle"
        fill="var(--color-dim)"
        fontFamily="ui-monospace, SFMono-Regular, monospace"
      >
        {label}
      </text>
    </g>
  );
}

// ---- React Flow node / edge types ----------------------------------

const NODE_META: Record<string, { icon: typeof Bot; stroke: string }> = {
  inference: { icon: Bot, stroke: '#4f8cff' },
  script: { icon: Terminal, stroke: '#34d399' },
  tool: { icon: Wrench, stroke: '#a78bfa' },
};

type GraphNodeData = {
  node: GraphNodeDTO;
  entry: boolean;
  onSelect: (id: string) => void;
};

function GraphNodeCard({ data, selected }: NodeProps<RFNode<GraphNodeData>>) {
  const { node, entry, onSelect } = data;
  if (node.id === '__end__') {
    return (
      <div
        onClick={() => onSelect(node.id)}
        className={`grid h-[34px] w-[34px] cursor-pointer place-items-center rounded-full border-2 bg-err/10 text-[9px] font-semibold text-err ${
          selected ? 'border-err' : 'border-edge'
        }`}
      >
        END
        {[0, 1, 2].map((i) => (
          <Handle
            key={`end-target-${i}`}
            type="target"
            position={Position.Left}
            id={`target-${i}`}
            style={{ top: 8 + i * 9, opacity: 0 }}
          />
        ))}
      </div>
    );
  }
  const meta = NODE_META[node.type] ?? {
    icon: CircleDashed,
    stroke: '#8b93a3',
  };
  const Icon = meta.icon;
  return (
    <div
      onClick={() => onSelect(node.id)}
      className={`flex h-full w-full cursor-pointer items-center gap-2.5 rounded-xl border bg-panel px-3 py-2 shadow-lg transition-shadow ${
        selected
          ? 'shadow-[0_0_0_3px_rgba(79,140,255,0.25)]'
          : 'hover:shadow-xl'
      }`}
      style={{
        borderColor: meta.stroke,
        borderWidth: selected ? 2 : 1,
        opacity: selected ? 1 : 0.92,
      }}
    >
      {[0, 1, 2].map((i) => (
        <Handle
          key={`source-${i}`}
          type="source"
          position={Position.Right}
          id={`source-${i}`}
          style={{ top: 16 + i * 16, opacity: 0 }}
        />
      ))}
      {[0, 1, 2].map((i) => (
        <Handle
          key={`target-${i}`}
          type="target"
          position={Position.Left}
          id={`target-${i}`}
          style={{ top: 16 + i * 16, opacity: 0 }}
        />
      ))}
      {[0, 1, 2].map((i) => (
        <Handle
          key={`back-${i}`}
          type="target"
          position={Position.Right}
          id={`back-${i}`}
          // Staggered below the source slots so a loop edge re-enters
          // at a different height than the forward edge leaves.
          style={{ top: 24 + i * 16, opacity: 0 }}
        />
      ))}
      <span
        className="grid h-8 w-8 shrink-0 place-items-center rounded-lg"
        style={{ background: `${meta.stroke}1f`, color: meta.stroke }}
      >
        <Icon size={16} />
      </span>
      <span className="min-w-0">
        <span className="block truncate text-sm font-semibold text-fg">
          {node.id}
        </span>
        <span className="block truncate text-[11px] text-dim">{node.type}</span>
      </span>
      {entry && (
        <span className="ml-auto grid h-4 w-4 shrink-0 place-items-center rounded-full border border-ok/60 bg-ok/10">
          <span className="h-1.5 w-1.5 rounded-full bg-ok" />
        </span>
      )}
    </div>
  );
}

type GraphEdgeData = {
  kind: 'forward' | 'side' | 'back';
  loopIndex: number;
  condition?: string;
};

function GraphEdge({
  sourceX,
  sourceY,
  targetX,
  targetY,
  data,
  selected,
}: EdgeProps<RFEdge<GraphEdgeData>>) {
  const { kind, loopIndex, condition } = (data ?? {}) as GraphEdgeData;
  const d = edgePathCoords(sourceX, sourceY, targetX, targetY, kind, loopIndex);
  const mid = edgeMid(sourceX, sourceY, targetX, targetY, kind, loopIndex);
  const color = selected
    ? 'var(--color-accent)'
    : kind === 'back'
      ? 'var(--color-dim)'
      : 'var(--color-edge)';
  return (
    <>
      <defs>
        <marker
          id="rf-arrow-edge"
          viewBox="0 0 10 10"
          refX="9"
          refY="5"
          markerWidth="6"
          markerHeight="6"
          orient="auto"
        >
          <path d="M 0 0 L 10 5 L 0 10 z" fill="#8b93a3" />
        </marker>
        <marker
          id="rf-arrow-accent"
          viewBox="0 0 10 10"
          refX="9"
          refY="5"
          markerWidth="6"
          markerHeight="6"
          orient="auto"
        >
          <path d="M 0 0 L 10 5 L 0 10 z" fill="#4f8cff" />
        </marker>
      </defs>
      <BaseEdge
        path={d}
        style={{
          stroke: color,
          strokeWidth: selected ? 2 : kind === 'back' ? 1.4 : 1.6,
          strokeDasharray: condition ? '5 4' : undefined,
        }}
        markerEnd={`url(#${selected ? 'rf-arrow-accent' : 'rf-arrow-edge'})`}
      />
      {condition && <ConditionPill condition={condition} x={mid.x} y={mid.y} />}
    </>
  );
}

const nodeTypes = { graphNode: GraphNodeCard };
const edgeTypes = { graphEdge: GraphEdge };

// ---- generic config editor -----------------------------------------

function setPath(
  obj: Record<string, unknown>,
  path: string[],
  value: unknown,
): Record<string, unknown> {
  const clone: Record<string, unknown> = { ...obj };
  let cur = clone;
  for (let i = 0; i < path.length - 1; i++) {
    const key = path[i];
    const next = (cur[key] as Record<string, unknown> | undefined) ?? {};
    const nextClone: Record<string, unknown> = { ...next };
    cur[key] = nextClone;
    cur = nextClone;
  }
  cur[path[path.length - 1]] = value;
  return clone;
}

function inferKind(
  value: unknown,
): 'bool' | 'number' | 'string' | 'array' | 'object' {
  if (typeof value === 'boolean') return 'bool';
  if (typeof value === 'number') return 'number';
  if (typeof value === 'string') return 'string';
  if (Array.isArray(value)) return 'array';
  if (value !== null && typeof value === 'object') return 'object';
  return 'string';
}

// FieldSpec describes one valid config field of a node type (mirrors
// the flowcraft node config structs). The editor uses the catalog to
// offer fields that are not yet present in the node's config.
type FieldSpec = {
  key: string;
  kind: 'string' | 'number' | 'bool' | 'array' | 'object';
};

const FIELD_CATALOGS: Record<string, FieldSpec[]> = {
  inference: [
    { key: 'model', kind: 'object' },
    { key: 'model_hint', kind: 'string' },
    { key: 'messages_channel', kind: 'string' },
    { key: 'system_prompt', kind: 'string' },
    { key: 'output_key', kind: 'string' },
    { key: 'usage_key', kind: 'string' },
    { key: 'tool_pending_key', kind: 'string' },
    { key: 'undefined_tool_recovery', kind: 'object' },
    { key: 'recover_pending_key', kind: 'string' },
    { key: 'recover_count_key', kind: 'string' },
    { key: 'stream', kind: 'bool' },
    { key: 'tools', kind: 'array' },
    { key: 'all_tools', kind: 'bool' },
    { key: 'tool_choice', kind: 'object' },
    { key: 'intent', kind: 'object' },
    { key: 'extensions', kind: 'array' },
  ],
  tool: [
    { key: 'messages_channel', kind: 'string' },
    { key: 'results_key', kind: 'string' },
  ],
  script: [
    { key: 'runtime', kind: 'string' },
    { key: 'name', kind: 'string' },
    { key: 'source', kind: 'string' },
    { key: 'config', kind: 'object' },
  ],
};

// Nested catalogs for object fields whose contents are also known.
const NESTED_FIELDS: Record<string, FieldSpec[]> = {
  'inference.undefined_tool_recovery': [
    { key: 'enabled', kind: 'bool' },
    { key: 'max_per_run', kind: 'number' },
  ],
  'script.config': [
    { key: 'preserve_recent', kind: 'number' },
    { key: 'budget_chars', kind: 'number' },
    { key: 'threshold_ratio', kind: 'number' },
    { key: 'max_compactions', kind: 'number' },
    { key: 'max_input_tokens', kind: 'number' },
    { key: 'system_prompt_tokens', kind: 'number' },
  ],
};

function defaultFor(spec: FieldSpec): unknown {
  switch (spec.kind) {
    case 'bool':
      return false;
    case 'number':
      return 0;
    case 'array':
      return [];
    case 'object':
      return {};
    default:
      return '';
  }
}

function AddFieldSelect({
  specs,
  onAdd,
}: {
  specs: FieldSpec[];
  onAdd: (spec: FieldSpec) => void;
}) {
  const { t } = useTranslation();
  if (specs.length === 0) return null;
  return (
    <select
      value=""
      onChange={(e) => {
        const spec = specs.find((s) => s.key === e.target.value);
        if (spec) onAdd(spec);
      }}
      className="w-full rounded-lg border border-dashed border-edge bg-panel px-2 py-1 text-xs text-dim outline-none focus:border-accent/60"
    >
      <option value="">＋ {t('graph.addField')}</option>
      {specs.map((s) => (
        <option key={s.key} value={s.key}>
          {s.key}
        </option>
      ))}
    </select>
  );
}

// Common field labels; anything not listed falls back to the raw key.
const FIELD_LABELS: Record<string, string> = {
  model_hint: 'graph.modelHint',
  all_tools: 'graph.allTools',
  stream: 'graph.stream',
  reasoning_effort: 'graph.reasoningEffort',
  max_per_run: 'graph.maxPerRun',
  tool_pending_key: 'graph.toolPendingKey',
  recover_pending_key: 'graph.recoverPendingKey',
  recover_count_key: 'graph.recoverCountKey',
  runtime: 'graph.runtime',
  preserve_recent: 'graph.preserveRecent',
  budget_chars: 'graph.budgetChars',
  threshold_ratio: 'graph.thresholdRatio',
  max_compactions: 'graph.maxCompactions',
  max_input_tokens: 'graph.maxInputTokens',
  system_prompt_tokens: 'graph.systemPromptTokens',
  results_key: 'graph.resultsKey',
};

function FieldLabel({ raw }: { raw: string }) {
  const { t } = useTranslation();
  const key = FIELD_LABELS[raw];
  return key ? <>{t(key)}</> : <span className="font-mono">{raw}</span>;
}

function GenericField({
  raw,
  path,
  value,
  nodeType,
  onChange,
}: {
  raw: string;
  path: string[];
  value: unknown;
  nodeType: string;
  onChange: (path: string[], value: unknown) => void;
}) {
  const { t } = useTranslation();
  const kind = inferKind(value);
  if (kind === 'object') {
    const entries = Object.entries(value as Record<string, unknown>);
    const nestedKey = `${nodeType}.${path.join('.')}`;
    const nestedCatalog = NESTED_FIELDS[nestedKey] ?? [];
    const presentNested = new Set(entries.map(([k]) => k));
    return (
      <details className="overflow-hidden rounded-lg border border-edge bg-panel/60">
        <summary className="cursor-pointer select-none px-2.5 py-1.5 text-xs text-dim">
          <FieldLabel raw={raw} />
          {entries.length === 0 && (
            <span className="ml-1 text-[10px] text-dim/60">
              ({t('graph.empty')})
            </span>
          )}
        </summary>
        <div className="space-y-2 border-t border-edge p-2">
          {entries.map(([k, v]) => (
            <GenericField
              key={k}
              raw={k}
              path={[...path, k]}
              value={v}
              nodeType={nodeType}
              onChange={onChange}
            />
          ))}
          <AddFieldSelect
            specs={nestedCatalog.filter((s) => !presentNested.has(s.key))}
            onAdd={(spec) => onChange([...path, spec.key], defaultFor(spec))}
          />
        </div>
      </details>
    );
  }
  if (kind === 'array') {
    return (
      <ArrayField raw={raw} path={path} value={value} onChange={onChange} />
    );
  }
  if (kind === 'bool') {
    return (
      <label className="flex items-center justify-between gap-2 text-xs text-dim">
        <FieldLabel raw={raw} />
        <input
          type="checkbox"
          checked={Boolean(value)}
          onChange={(e) => onChange(path, e.target.checked)}
          className="h-4 w-4 accent-[var(--color-accent)]"
        />
      </label>
    );
  }
  return (
    <label className="flex flex-col gap-1 text-xs text-dim">
      <FieldLabel raw={raw} />
      <input
        type={kind === 'number' ? 'number' : 'text'}
        value={value == null ? '' : String(value)}
        onChange={(e) =>
          onChange(
            path,
            kind === 'number' ? Number(e.target.value) : e.target.value,
          )
        }
        className="rounded-lg border border-edge bg-panel px-2.5 py-1.5 font-mono text-xs text-fg outline-none focus:border-accent/60"
      />
    </label>
  );
}

function ArrayField({
  raw,
  path,
  value,
  onChange,
}: {
  raw: string;
  path: string[];
  value: unknown;
  onChange: (path: string[], value: unknown) => void;
}) {
  const { t } = useTranslation();
  const [text, setText] = useState(JSON.stringify(value, null, 2));
  const [invalid, setInvalid] = useState(false);
  useEffect(() => {
    setText(JSON.stringify(value, null, 2));
    setInvalid(false);
  }, [value]);
  return (
    <label className="flex flex-col gap-1 text-xs text-dim">
      <FieldLabel raw={raw} />
      <textarea
        value={text}
        spellCheck={false}
        rows={3}
        onChange={(e) => {
          setText(e.target.value);
          try {
            const parsed = JSON.parse(e.target.value);
            if (!Array.isArray(parsed)) throw new Error('not an array');
            setInvalid(false);
            onChange(path, parsed);
          } catch {
            setInvalid(true);
          }
        }}
        className={`w-full resize-y rounded-lg border bg-panel px-2 py-1 font-mono text-[11px] text-fg outline-none ${
          invalid ? 'border-err/60' : 'border-edge'
        }`}
      />
      {invalid && (
        <span className="text-[10px] text-err">{t('graph.invalidJson')}</span>
      )}
    </label>
  );
}

// ---- canvas ---------------------------------------------------------

function GraphCanvas({
  rfNodes,
  rfEdges,
  onNodesChange,
  onNodeClick,
  onEdgeClick,
  onPaneClick,
  onRelayout,
}: {
  rfNodes: RFNode<GraphNodeData>[];
  rfEdges: RFEdge<GraphEdgeData>[];
  onNodesChange: (changes: NodeChange<RFNode<GraphNodeData>>[]) => void;
  onNodeClick: (id: string) => void;
  onEdgeClick: (from: string, to: string) => void;
  onPaneClick: () => void;
  onRelayout: () => void;
}) {
  const { t } = useTranslation();
  const { fitView } = useReactFlow();
  useEffect(() => {
    if (rfNodes.length === 0) return;
    const raf = requestAnimationFrame(() => void fitView({ padding: 0.2 }));
    return () => cancelAnimationFrame(raf);
  }, [rfNodes.length, fitView]);
  return (
    <div className="relative min-w-0 flex-1 overflow-hidden rounded-xl border border-edge bg-panel2/40">
      <ReactFlow
        nodes={rfNodes}
        edges={rfEdges}
        nodeTypes={nodeTypes}
        edgeTypes={edgeTypes}
        onNodesChange={onNodesChange}
        onNodeClick={(_, n) => onNodeClick(n.id)}
        onEdgeClick={(_, e) => onEdgeClick(e.source, e.target)}
        onPaneClick={onPaneClick}
        fitView
        fitViewOptions={{ padding: 0.2 }}
        minZoom={0.1}
        maxZoom={2.5}
        nodesConnectable={false}
        deleteKeyCode={null}
      >
        <Background
          variant={BackgroundVariant.Dots}
          gap={26}
          size={1}
          color="rgba(139,147,163,0.28)"
        />
        <Controls
          showInteractive={false}
          style={
            {
              '--xy-controls-button-background-color': 'var(--color-panel)',
              '--xy-controls-button-background-color-hover':
                'var(--color-panel2)',
              '--xy-controls-button-color': 'var(--color-dim)',
              '--xy-controls-button-color-hover': 'var(--color-fg)',
              '--xy-controls-button-border-color': 'var(--color-edge)',
              '--xy-controls-box-shadow': '0 4px 14px rgba(0,0,0,0.45)',
            } as CSSProperties
          }
        />
        <MiniMap
          pannable
          zoomable
          nodeColor={(n) => {
            const d = n.data as GraphNodeData | undefined;
            return NODE_META[d?.node?.type ?? '']?.stroke ?? '#8b93a3';
          }}
          style={
            {
              '--xy-minimap-background-color': 'var(--color-panel2)',
              '--xy-minimap-mask-background-color': 'rgba(15,18,24,0.8)',
              '--xy-minimap-mask-stroke-color': 'var(--color-edge)',
              '--xy-minimap-node-stroke-color': 'var(--color-edge)',
              border: '1px solid var(--color-edge)',
              borderRadius: 8,
            } as CSSProperties
          }
        />
      </ReactFlow>
      <button
        onClick={() => {
          onRelayout();
          void fitView({ padding: 0.2 });
        }}
        className="absolute left-2 top-2 z-10 rounded-lg border border-edge bg-panel/90 px-2.5 py-1 text-xs text-dim hover:bg-panel2 hover:text-fg"
      >
        {t('graph.relayout')}
      </button>
    </div>
  );
}

// ---- editor ---------------------------------------------------------

export function AgentGraphEditor({
  agentName,
  onClose,
  onSaved,
}: {
  agentName: string;
  onClose: () => void;
  onSaved: () => void;
}) {
  const { t } = useTranslation();
  const [graph, setGraph] = useState<GraphDTO | null>(null);
  const [description, setDescription] = useState('');
  const [error, setError] = useState('');
  const [selected, setSelected] = useState<
    | { kind: 'node'; id: string }
    | { kind: 'edge'; from: string; to: string }
    | null
  >(null);
  const [saving, setSaving] = useState(false);
  const [saved, setSaved] = useState(false);
  const [rawDraft, setRawDraft] = useState('');
  const [rawTouched, setRawTouched] = useState(false);
  const [edgeCondition, setEdgeCondition] = useState('');
  const [rfNodes, setRfNodes] = useState<RFNode<GraphNodeData>[]>([]);
  const [rfEdges, setRfEdges] = useState<RFEdge<GraphEdgeData>[]>([]);

  const load = async () => {
    try {
      const detail = await api.agentDetail(agentName);
      setGraph(detail.graph);
      setDescription(detail.description);
      setSelected(null);
      setError('');
    } catch (err) {
      setError(String(err));
    }
  };
  useEffect(() => {
    void load();
  }, [agentName]);

  const pos = useMemo(() => (graph ? placeNodes(graph) : new Map()), [graph]);
  const structureKey = useMemo(
    () =>
      graph
        ? JSON.stringify({
            e: graph.entry,
            n: graph.nodes.map((n) => n.id),
            g: graph.edges.map((e) => e.from + '>' + e.to),
          })
        : '',
    [graph],
  );
  const backOrder = useMemo(() => {
    const order = new Map<string, number>();
    let n = 0;
    for (const e of graph?.edges ?? []) {
      const a = pos.get(e.from);
      const b = pos.get(e.to);
      if (a && b && (b.layer ?? 0) < (a.layer ?? 0)) {
        order.set(`${e.from}->${e.to}`, n++);
      }
    }
    return order;
  }, [graph, pos]);

  // Rebuild React Flow state only when the graph structure changes
  // (load/save), so manual node dragging survives inspector edits.
  useEffect(() => {
    if (!graph) return;
    setRfNodes(
      graph.nodes.map((n) => {
        const p = pos.get(n.id);
        return {
          id: n.id,
          type: 'graphNode',
          position: { x: p?.x ?? 0, y: p?.y ?? 0 },
          width: NODE_W,
          height: NODE_H,
          data: {
            node: n,
            entry: n.id === graph.entry,
            onSelect: selectNode,
          },
        };
      }),
    );
    const end = pos.get('__end__');
    if (end) {
      setRfNodes((nds) => [
        ...nds,
        {
          id: '__end__',
          type: 'graphNode',
          position: { x: end.x, y: end.y },
          width: END_W,
          height: END_W,
          data: {
            node: { id: '__end__', type: 'end' },
            entry: false,
            onSelect: (id) => setSelected({ kind: 'node', id }),
          },
        },
      ]);
    }
    // Fan out parallel edges across the three handle slots so edges
    // leaving the same node (or entering the same node) never share a
    // connection point.
    const sourceCount = new Map<string, number>();
    const targetCount = new Map<string, number>();
    setRfEdges(
      graph.edges.map((e) => {
        const a = pos.get(e.from);
        const b = pos.get(e.to);
        const back = (b?.layer ?? 0) < (a?.layer ?? 0);
        const side = (b?.layer ?? 0) === (a?.layer ?? 0);
        const si = sourceCount.get(e.from) ?? 0;
        sourceCount.set(e.from, si + 1);
        const ti = targetCount.get(e.to) ?? 0;
        targetCount.set(e.to, ti + 1);
        return {
          id: `${e.from}->${e.to}`,
          source: e.from,
          target: e.to,
          sourceHandle: `source-${si % 3}`,
          targetHandle: back || side ? `back-${ti % 3}` : `target-${ti % 3}`,
          type: 'graphEdge',
          data: {
            kind: back ? 'back' : side ? 'side' : 'forward',
            loopIndex: backOrder.get(`${e.from}->${e.to}`) ?? 0,
            condition: e.condition,
          },
        };
      }),
    );
  }, [structureKey]);

  const onNodesChange = (changes: NodeChange<RFNode<GraphNodeData>>[]) =>
    setRfNodes((nds) => applyNodeChanges(changes, nds));

  const selectNode = (id: string) => {
    const node = graph?.nodes.find((n) => n.id === id);
    if (!node) return;
    setSelected({ kind: 'node', id });
    setRawDraft(JSON.stringify(node.config ?? {}, null, 2));
    setRawTouched(false);
  };

  const selectEdge = (from: string, to: string) => {
    const edge = graph?.edges.find((e) => e.from === from && e.to === to);
    if (!edge) return;
    setSelected({ kind: 'edge', from, to });
    setEdgeCondition(edge.condition ?? '');
  };

  const updateNodeField = (nodeID: string, path: string[], value: unknown) => {
    if (!graph) return;
    setGraph({
      ...graph,
      nodes: graph.nodes.map((n) =>
        n.id === nodeID
          ? {
              ...n,
              config: setPath(
                (n.config as Record<string, unknown>) ?? {},
                path,
                value,
              ),
            }
          : n,
      ),
    });
  };

  const updateEdgeCondition = (condition: string) => {
    if (!graph || selected?.kind !== 'edge') return;
    setEdgeCondition(condition);
    setGraph({
      ...graph,
      edges: graph.edges.map((e) =>
        e.from === selected.from && e.to === selected.to
          ? { ...e, condition: condition || undefined }
          : e,
      ),
    });
  };

  const save = async () => {
    if (!graph) return;
    setSaving(true);
    setSaved(false);
    try {
      let nodes = graph.nodes;
      if (selected?.kind === 'node' && rawTouched) {
        const parsed = JSON.parse(rawDraft) as Record<string, unknown>;
        nodes = graph.nodes.map((n) =>
          n.id === selected.id ? { ...n, config: parsed } : n,
        );
        setRawTouched(false);
      }
      await api.updateAgent(
        agentName,
        description.trim(),
        JSON.stringify({ ...graph, nodes }),
      );
      onSaved();
      setSaved(true);
      await load();
    } catch (err) {
      setError(String(err));
    } finally {
      setSaving(false);
    }
  };

  const selectedNode =
    selected?.kind === 'node'
      ? graph?.nodes.find((n) => n.id === selected.id)
      : undefined;
  const selectedEdge =
    selected?.kind === 'edge'
      ? graph?.edges.find(
          (e) => e.from === selected.from && e.to === selected.to,
        )
      : undefined;

  return (
    <div className="flex h-full min-h-0 flex-col gap-3">
      <div className="flex items-center gap-2">
        <Workflow size={14} className="text-accent shrink-0" />
        <h3 className="text-sm font-semibold truncate">
          {t('graph.editing', { name: agentName })}
        </h3>
        {saved && <span className="text-xs text-ok">{t('graph.saved')}</span>}
        <span className="flex-1" />
        <button
          onClick={onClose}
          className="text-dim hover:text-fg"
          title={t('graph.back')}
        >
          <X size={16} />
        </button>
      </div>
      <label className="flex flex-col gap-1 text-xs text-dim">
        {t('graph.description')}
        <input
          value={description}
          onChange={(e) => setDescription(e.target.value)}
          className="rounded-lg border border-edge bg-panel2 px-2.5 py-1.5 text-xs text-fg outline-none focus:border-accent/60"
        />
      </label>
      {error && <p className="text-xs text-err">{error}</p>}

      {!graph ? (
        <div className="grid h-48 place-items-center text-dim">
          <Loader2 size={18} className="animate-spin" />
        </div>
      ) : (
        <div className="flex min-h-0 flex-1 gap-3">
          <ReactFlowProvider>
            <GraphCanvas
              rfNodes={rfNodes}
              rfEdges={rfEdges}
              onNodesChange={onNodesChange}
              onNodeClick={(id) => selectNode(id)}
              onEdgeClick={(from, to) => selectEdge(from, to)}
              onPaneClick={() => setSelected(null)}
              onRelayout={() => {
                const fresh = placeNodes(graph);
                setRfNodes((nds) =>
                  nds.map((n) => {
                    const p = fresh.get(n.id);
                    return p ? { ...n, position: { x: p.x, y: p.y } } : n;
                  }),
                );
              }}
            />
          </ReactFlowProvider>

          <div className="flex w-72 shrink-0 flex-col gap-3 overflow-y-auto rounded-xl border border-edge bg-panel2/60 p-3">
            {selected?.kind === 'node' && selectedNode ? (
              <NodeInspector
                node={selectedNode}
                entry={selectedNode.id === graph.entry}
                rawDraft={rawDraft}
                rawTouched={rawTouched}
                onField={(path, value) =>
                  updateNodeField(selectedNode.id, path, value)
                }
                onRaw={(v) => {
                  setRawDraft(v);
                  setRawTouched(true);
                }}
                onClose={() => setSelected(null)}
              />
            ) : selected?.kind === 'edge' && selectedEdge ? (
              <div className="flex flex-col gap-3">
                <div className="flex items-center gap-1.5 text-sm font-medium">
                  <Braces size={13} className="text-accent" />
                  {selectedEdge.from} → {selectedEdge.to}
                </div>
                <label className="flex flex-col gap-1 text-xs text-dim">
                  {t('graph.condition')}
                  <input
                    value={edgeCondition}
                    onChange={(e) => updateEdgeCondition(e.target.value)}
                    placeholder="tool_pending == true"
                    className="rounded-lg border border-edge bg-panel px-2.5 py-1.5 text-xs text-fg outline-none focus:border-accent/60 font-mono"
                  />
                </label>
                <p className="text-[10px] text-dim leading-relaxed">
                  {t('graph.conditionHint')}
                </p>
                <div className="flex-1" />
                <SaveButton saving={saving} onSave={() => void save()} />
              </div>
            ) : (
              <div className="flex flex-1 items-center justify-center text-xs text-dim">
                {t('graph.selectHint')}
              </div>
            )}
            {selected && selected.kind === 'node' && selectedNode && (
              <SaveButton saving={saving} onSave={() => void save()} />
            )}
          </div>
        </div>
      )}
    </div>
  );
}

function SaveButton({
  saving,
  onSave,
}: {
  saving: boolean;
  onSave: () => void;
}) {
  const { t } = useTranslation();
  return (
    <button
      onClick={onSave}
      disabled={saving}
      className="flex items-center justify-center gap-1.5 rounded-lg bg-accent px-3 py-1.5 text-sm text-white hover:opacity-90 disabled:opacity-50"
    >
      {saving ? (
        <Loader2 size={13} className="animate-spin" />
      ) : (
        <Save size={13} />
      )}
      {t('graph.save')}
    </button>
  );
}

function NodeInspector({
  node,
  entry,
  rawDraft,
  rawTouched,
  onField,
  onRaw,
  onClose,
}: {
  node: GraphNodeDTO;
  entry: boolean;
  rawDraft: string;
  rawTouched: boolean;
  onField: (path: string[], value: unknown) => void;
  onRaw: (v: string) => void;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const config = (node.config ?? {}) as Record<string, unknown>;
  const entries = Object.entries(config);
  return (
    <div className="flex min-h-0 flex-col gap-2.5">
      <div className="flex items-center gap-1.5">
        <span className="truncate text-sm font-medium">{node.id}</span>
        {entry && (
          <span className="rounded border border-ok/40 bg-ok/10 px-1 py-0.5 text-[10px] text-ok">
            {t('graph.entry')}
          </span>
        )}
        <span className="text-xs text-dim">{node.type}</span>
        <span className="flex-1" />
        <button onClick={onClose} className="text-dim hover:text-fg">
          <span className="text-lg leading-none">×</span>
        </button>
      </div>
      {entries.length === 0 ? (
        <p className="text-xs text-dim">{t('graph.noConfig')}</p>
      ) : (
        <div className="space-y-2">
          {entries.map(([k, v]) => (
            <GenericField
              key={k}
              raw={k}
              path={[k]}
              value={v}
              nodeType={node.type}
              onChange={onField}
            />
          ))}
        </div>
      )}
      {(() => {
        const catalog = FIELD_CATALOGS[node.type] ?? [];
        const present = new Set(entries.map(([k]) => k));
        return (
          <AddFieldSelect
            specs={catalog.filter((s) => !present.has(s.key))}
            onAdd={(spec) => onField([spec.key], defaultFor(spec))}
          />
        );
      })()}
      <details className="rounded-lg border border-edge bg-panel/70">
        <summary className="cursor-pointer px-2.5 py-1.5 text-xs text-dim select-none">
          {t('graph.advanced')}
        </summary>
        <textarea
          value={rawDraft}
          onChange={(e) => onRaw(e.target.value)}
          spellCheck={false}
          rows={10}
          className="w-full resize-y bg-transparent px-2.5 pb-2 text-[11px] font-mono text-fg outline-none whitespace-pre"
        />
      </details>
      {rawTouched && (
        <p className="text-[10px] text-warn">{t('graph.rawEdited')}</p>
      )}
    </div>
  );
}
