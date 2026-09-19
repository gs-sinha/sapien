// Read-only flow chart (PLAN §34f item 9): renders layout.ts's positioned
// nodes/edges as hand-rolled SVG. No graph/diagram library and no
// hard-coded colours -- status fill/stroke reuse the same palette
// StatusPill uses (see NODE_STATUS_CLASS below), and everything else is
// slate/sky (ui/tailwind.config.js: slate is the stone neutral, sky the
// moss accent), so it follows dark mode for free.
//
// This file (plus layout.ts/statuses.ts) is meant to be imported with
// React.lazy from FlowDetailPage/RunDetailPage so it stays its own chunk.
import { useId, useMemo, useState } from 'react';
import type { KeyboardEvent } from 'react';
import {
  CONTAINER_HEADER,
  LANE_LABEL_HEIGHT,
  MARGIN,
  layoutFlow,
} from './layout';
import type { ChartFlow, EdgeKind, LayoutEdge, LayoutNode, Point } from './layout';
import { iterationFailed } from './statuses';
import type { BlockRunInfo, FlowChartStatuses, StepStatusInfo } from './statuses';

// Same palette as components/StatusPill.tsx, spelled out as fill/stroke
// utilities (Tailwind's content scanner needs the full class name literal
// somewhere in the source -- see ui/tailwind.config.js's content globs).
const NODE_STATUS_CLASS: Record<string, string> = {
  passed: 'fill-emerald-50 stroke-emerald-500 dark:fill-emerald-950/60 dark:stroke-emerald-400',
  ok: 'fill-emerald-50 stroke-emerald-500 dark:fill-emerald-950/60 dark:stroke-emerald-400',
  failed: 'fill-red-50 stroke-red-500 dark:fill-red-950/60 dark:stroke-red-400',
  errored: 'fill-red-50 stroke-red-500 dark:fill-red-950/60 dark:stroke-red-400',
  running: 'fill-blue-50 stroke-blue-500 dark:fill-blue-950/60 dark:stroke-blue-400',
  requesting: 'fill-blue-50 stroke-blue-500 dark:fill-blue-950/60 dark:stroke-blue-400',
  polling: 'fill-blue-50 stroke-blue-500 dark:fill-blue-950/60 dark:stroke-blue-400',
  asserting: 'fill-blue-50 stroke-blue-500 dark:fill-blue-950/60 dark:stroke-blue-400',
  resolving: 'fill-blue-50 stroke-blue-500 dark:fill-blue-950/60 dark:stroke-blue-400',
  queued: 'fill-white stroke-slate-300 dark:fill-slate-900 dark:stroke-slate-700',
  pending: 'fill-white stroke-slate-300 dark:fill-slate-900 dark:stroke-slate-700',
  cancelled: 'fill-amber-50 stroke-amber-500 dark:fill-amber-950/60 dark:stroke-amber-400',
  skipped: 'fill-slate-100 stroke-slate-300 dark:fill-slate-800 dark:stroke-slate-600',
};
const DEFAULT_NODE_CLASS = 'fill-white stroke-slate-300 dark:fill-slate-900 dark:stroke-slate-700';

export interface FlowChartProps {
  flow: ChartFlow;
  statuses?: FlowChartStatuses;
  selectedId?: string;
  onSelect?: (stepId: string) => void;
}

function midPoint(points: Point[]): Point {
  if (points.length <= 2) {
    const [a, b] = points;
    return b ? { x: (a.x + b.x) / 2, y: (a.y + b.y) / 2 } : a;
  }
  return { x: (points[1].x + points[2].x) / 2, y: (points[1].y + points[2].y) / 2 };
}

function cornerCut(corner: Point, toward: Point, r: number): Point {
  const dx = toward.x - corner.x;
  const dy = toward.y - corner.y;
  const len = Math.hypot(dx, dy) || 1;
  const rr = Math.min(r, len / 2);
  return { x: corner.x + (dx / len) * rr, y: corner.y + (dy / len) * rr };
}

/** Straight line for a 2-point edge; a rounded elbow (two quadratic
 * corners) for a 4-point one (the `false`/`back` edges layout.ts routes
 * around a rail). */
function edgePath(points: Point[]): string {
  if (points.length !== 4) {
    return points.map((p, i) => `${i === 0 ? 'M' : 'L'}${p.x},${p.y}`).join(' ');
  }
  const [p0, p1, p2, p3] = points;
  const r = 10;
  const a = cornerCut(p1, p0, r);
  const b = cornerCut(p1, p2, r);
  const c = cornerCut(p2, p1, r);
  const d = cornerCut(p2, p3, r);
  return `M${p0.x},${p0.y} L${a.x},${a.y} Q${p1.x},${p1.y} ${b.x},${b.y} L${c.x},${c.y} Q${p2.x},${p2.y} ${d.x},${d.y} L${p3.x},${p3.y}`;
}

function diamondPoints(n: LayoutNode): string {
  const cx = n.x + n.width / 2;
  const cy = n.y + n.height / 2;
  return `${cx},${n.y} ${n.x + n.width},${cy} ${cx},${n.y + n.height} ${n.x},${cy}`;
}

export function FlowChart({ flow, statuses, selectedId, onSelect }: FlowChartProps) {
  const layout = useMemo(() => layoutFlow(flow), [flow]);
  const uid = useId();
  const arrowId = `fc-arrow-${uid.replace(/[^a-zA-Z0-9_-]/g, '')}`;
  // Which iteration each loop block's picker is showing; a block absent
  // here falls back to "first failed, else the last iteration" (below).
  const [selectedIteration, setSelectedIteration] = useState<Record<string, number>>({});

  const nodeByStepId = useMemo(() => {
    const m = new Map<string, LayoutNode>();
    for (const n of layout.nodes) if (n.kind !== 'when') m.set(n.stepId, n);
    return m;
  }, [layout.nodes]);

  function currentIteration(stepId: string, block: BlockRunInfo): number {
    const chosen = selectedIteration[stepId];
    if (chosen !== undefined && chosen < block.iterations.length) return chosen;
    const firstFailed = block.iterations.findIndex(iterationFailed);
    if (firstFailed >= 0) return firstFailed;
    return Math.max(0, block.iterations.length - 1);
  }

  function statusFor(node: LayoutNode): StepStatusInfo | undefined {
    if (node.kind === 'block') return statuses?.blocks[node.stepId];
    if (node.parentContainer) {
      const block = statuses?.blocks[node.parentContainer];
      if (!block || block.iterations.length === 0) return undefined;
      const idx = currentIteration(node.parentContainer, block);
      return block.iterations[idx]?.[node.stepId];
    }
    if (node.kind === 'call') return statuses?.steps[node.stepId];
    return undefined;
  }

  function guardStatusFor(stepId: string): StepStatusInfo | undefined {
    const node = nodeByStepId.get(stepId);
    return node ? statusFor(node) : undefined;
  }

  const containerNodes = layout.nodes.filter((n) => n.kind === 'block');
  const otherNodes = layout.nodes.filter((n) => n.kind !== 'block');

  const select = (stepId: string) => onSelect?.(stepId);
  const onNodeKeyDown = (e: KeyboardEvent, stepId: string) => {
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault();
      select(stepId);
    }
  };

  const renderContainer = (n: LayoutNode) => {
    const info = statusFor(n);
    const cls = (info?.status && NODE_STATUS_CLASS[info.status]) || DEFAULT_NODE_CLASS;
    const selected = selectedId === n.stepId;
    return (
      <g
        key={n.id}
        tabIndex={0}
        role="button"
        aria-label={`step ${n.stepId}${info?.status ? `, ${info.status}` : ''}`}
        onClick={() => select(n.stepId)}
        onKeyDown={(e) => onNodeKeyDown(e, n.stepId)}
        className={`cursor-pointer outline-none ${info?.status === 'skipped' ? 'opacity-50' : ''}`}
        data-node-kind="block"
        data-step-id={n.stepId}
      >
        <rect
          x={n.x}
          y={n.y}
          width={n.width}
          height={n.height}
          rx={12}
          strokeWidth={selected ? 2.5 : 1.5}
          strokeDasharray="6 4"
          className={`${cls} ${selected ? 'stroke-sky-600 dark:stroke-sky-400' : ''}`}
        />
        <text x={n.x + 12} y={n.y + 20} className="fill-slate-600 font-mono text-[11px] dark:fill-slate-300">
          {n.conditionText}
          <title>{n.conditionFull}</title>
        </text>
        {n.breakWhen && (
          <text x={n.x + 12} y={n.y + n.height - 6} className="fill-slate-400 text-[10px] dark:fill-slate-500">
            break: {n.breakWhen.length > 30 ? `${n.breakWhen.slice(0, 29)}…` : n.breakWhen}
          </text>
        )}
      </g>
    );
  };

  const renderCall = (n: LayoutNode) => {
    const info = statusFor(n);
    const cls = (info?.status && NODE_STATUS_CLASS[info.status]) || DEFAULT_NODE_CLASS;
    const selected = selectedId === n.stepId;
    return (
      <g
        key={n.id}
        tabIndex={0}
        role="button"
        aria-label={`step ${n.stepId}${info?.status ? `, ${info.status}` : ''}`}
        onClick={() => select(n.stepId)}
        onKeyDown={(e) => onNodeKeyDown(e, n.stepId)}
        className={`cursor-pointer outline-none ${info?.status === 'skipped' ? 'opacity-50' : ''}`}
        data-node-kind="call"
        data-step-id={n.stepId}
      >
        <rect x={n.x} y={n.y} width={n.width} height={n.height} rx={8} strokeWidth={selected ? 2.5 : 1.5} className={`${cls} ${selected ? 'stroke-sky-600 dark:stroke-sky-400' : ''}`} />
        <text x={n.x + n.width / 2} y={n.y + (n.sublabel ? 18 : n.height / 2 + 4)} textAnchor="middle" className="fill-slate-700 font-mono text-[12px] font-medium dark:fill-slate-200">
          {n.label}
        </text>
        {n.sublabel && (
          <text x={n.x + n.width / 2} y={n.y + 33} textAnchor="middle" className="fill-slate-400 font-mono text-[10px] dark:fill-slate-500">
            {n.sublabel}
          </text>
        )}
      </g>
    );
  };

  const renderDiamond = (n: LayoutNode) => (
    <g key={n.id} data-node-kind="when" data-step-id={n.stepId}>
      <polygon points={diamondPoints(n)} strokeWidth={1.5} className="fill-white stroke-slate-300 dark:fill-slate-900 dark:stroke-slate-600" />
      <text x={n.x + n.width / 2} y={n.y + n.height / 2 + 4} textAnchor="middle" className="fill-slate-500 text-[13px] font-semibold dark:fill-slate-400">
        ?
        <title>{n.conditionFull}</title>
      </text>
    </g>
  );

  const edgeClass = (e: LayoutEdge, kind: EdgeKind, skippedWhen: boolean): string => {
    if (kind === 'false' && skippedWhen) return 'stroke-amber-500 dark:stroke-amber-400';
    if (kind === 'true' && skippedWhen) return 'stroke-slate-300 opacity-40 dark:stroke-slate-700';
    if (kind === 'back') return 'stroke-sky-500 dark:stroke-sky-400';
    if (kind === 'false') return 'stroke-slate-400 dark:stroke-slate-600';
    void e;
    return 'stroke-slate-400 dark:stroke-slate-500';
  };

  return (
    <div className="w-full max-h-[70vh] overflow-auto rounded border border-slate-200 dark:border-slate-800">
      <div className="relative" style={{ width: layout.width, height: layout.height }}>
        <svg width={layout.width} height={layout.height} className="block" role="img" aria-label="Flow chart">
          <defs>
            <marker id={arrowId} viewBox="0 0 10 10" refX="8" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse">
              <path d="M0,0 L10,5 L0,10 Z" className="fill-slate-400 dark:fill-slate-500" />
            </marker>
          </defs>

          {layout.lanes.map((lane) => (
            <text
              key={lane.label}
              x={MARGIN}
              y={lane.y + LANE_LABEL_HEIGHT - 6}
              className="fill-slate-400 text-[11px] font-semibold uppercase tracking-wide dark:fill-slate-500"
            >
              {lane.label}
            </text>
          ))}

          {containerNodes.map(renderContainer)}

          {layout.edges.map((e) => {
            const guard = e.kind === 'true' || e.kind === 'false' ? guardStatusFor(e.stepId) : undefined;
            const skippedWhen = guard?.status === 'skipped' && guard.skipReason === 'when';
            const mid = midPoint(e.points);
            return (
              <g key={e.id}>
                <path
                  d={edgePath(e.points)}
                  fill="none"
                  strokeWidth={e.kind === 'false' && skippedWhen ? 2.5 : 1.5}
                  className={edgeClass(e, e.kind, skippedWhen)}
                  strokeDasharray={e.kind === 'false' || e.kind === 'back' ? '4 3' : undefined}
                  markerEnd={`url(#${arrowId})`}
                />
                {e.label && (
                  <text x={mid.x + 6} y={mid.y - 4} className="fill-slate-400 text-[10px] dark:fill-slate-500">
                    {e.label}
                  </text>
                )}
              </g>
            );
          })}

          {otherNodes.map((n) => (n.kind === 'when' ? renderDiamond(n) : renderCall(n)))}
        </svg>

        {containerNodes.map((n) => {
          const block = statuses?.blocks[n.stepId];
          if (!block || block.iterations.length === 0) return null;
          return (
            <LoopOverlay
              key={n.id}
              node={n}
              block={block}
              current={currentIteration(n.stepId, block)}
              onChange={(i) => setSelectedIteration((prev) => ({ ...prev, [n.stepId]: i }))}
            />
          );
        })}
      </div>
    </div>
  );
}

function LoopOverlay({
  node,
  block,
  current,
  onChange,
}: {
  node: LayoutNode;
  block: BlockRunInfo;
  current: number;
  onChange: (i: number) => void;
}) {
  const total = block.iterations.length;
  const failedCount = block.iterations.filter(iterationFailed).length;
  const passedCount = total - failedCount;
  const firstFailed = block.iterations.findIndex(iterationFailed);

  return (
    <div
      className="absolute flex items-center gap-1.5 overflow-hidden px-3 text-[11px] text-slate-500 dark:text-slate-400"
      style={{ left: node.x, top: node.y + (CONTAINER_HEADER - 26), width: node.width, height: 26 }}
    >
      <span className="truncate">
        &times;{block.count ?? total} &middot; {passedCount} passed{failedCount > 0 ? ` · ${failedCount} failed` : ''}
      </span>
      <span className="ml-auto flex items-center gap-1 whitespace-nowrap">
        <button
          type="button"
          aria-label="previous iteration"
          disabled={current <= 0}
          onClick={() => onChange(current - 1)}
          className="rounded border border-slate-300 px-1 leading-4 disabled:opacity-30 dark:border-slate-700"
        >
          &lsaquo;
        </button>
        <span>
          iteration {current + 1} of {total}
        </span>
        <button
          type="button"
          aria-label="next iteration"
          disabled={current >= total - 1}
          onClick={() => onChange(current + 1)}
          className="rounded border border-slate-300 px-1 leading-4 disabled:opacity-30 dark:border-slate-700"
        >
          &rsaquo;
        </button>
        {firstFailed >= 0 && (
          <button
            type="button"
            aria-label="jump to first failed iteration"
            onClick={() => onChange(firstFailed)}
            className="rounded border border-slate-300 px-1 leading-4 text-red-600 dark:border-slate-700 dark:text-red-400"
          >
            first failed
          </button>
        )}
      </span>
    </div>
  );
}

export default FlowChart;
