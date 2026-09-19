// Pure layout for the read-only flow chart (PLAN §34f item 9): turns a
// flow's structured control flow (setup, steps, teardown -- no arbitrary
// graph, since the DSL allows none: see docs/flows.md "Conditions and
// loops") into positioned nodes and edges for FlowChart.tsx to draw as
// hand-rolled SVG. No dependency on run data: statuses are overlaid at
// render time (components/flowchart/statuses.ts), so the same layout is
// reused for a flow's static definition and for any run of it.
//
// Shape: a single vertical spine. A step with `when` gets a diamond in
// front of it (a "true" edge into the step, a "false" edge bypassing it to
// the next node). A loop block (`foreach`/`repeat`) is a container holding
// its nested steps' own mini-spine, with a back edge from the last nested
// node up to the container's entry. Blocks cannot nest and are not allowed
// in setup/teardown (PLAN §34f.8), so recursion here only ever goes one
// level deep in practice, though the algorithm itself doesn't assume that.
import { blockHeaderText, isBlockStep } from '../../lib/flowStep';
import type { Step } from '../../api/types';

export const NODE_W = 200;
export const NODE_H = 44;
export const DIAMOND = 48;
export const V_GAP = 32;
export const CONTAINER_HEADER = 56; // condition text row + a reserved row for a run's count/iteration-picker overlay
export const CONTAINER_PAD_TOP = 16;
export const CONTAINER_PAD_BOTTOM = 20;
export const CONTAINER_PAD_SIDE = 32;
export const LANE_LABEL_HEIGHT = 22;
export const LANE_GAP = 36;
export const MARGIN = 24;

export type NodeKind = 'call' | 'block' | 'when';
export type Phase = 'setup' | 'steps' | 'teardown';

export interface LayoutNode {
  /** Unique within the chart: `${stepId}::call` | `::block` | `::when`. */
  id: string;
  /** The underlying flow step id (shared by a step's diamond and its main node). */
  stepId: string;
  kind: NodeKind;
  x: number;
  y: number;
  width: number;
  height: number;
  /** Step id (call/block) or, for a diamond, also the step id (label is rendered above the shape). */
  label: string;
  /** Operation id shown in mono under the label, for a call node only. */
  sublabel?: string;
  /** Truncated `when`/block-header text, shown on the node. */
  conditionText?: string;
  /** Untruncated text for a <title> tooltip. */
  conditionFull?: string;
  phase: Phase;
  /** The enclosing block's step id, for a node nested inside a container. */
  parentContainer?: string;
  /** Set on a block node when it has a `break_when`, for the exit label. */
  breakWhen?: string;
}

export type EdgeKind = 'flow' | 'true' | 'false' | 'back';

export interface Point {
  x: number;
  y: number;
}

export interface LayoutEdge {
  id: string;
  kind: EdgeKind;
  from: string;
  /** Absent for a `false` bypass edge off the very last step of a spine (nothing to point at but the spine's own exit point). */
  to?: string;
  /** The step id this edge belongs to (the guarded step for true/false, the block for back). */
  stepId: string;
  /** Path vertices, absolute coordinates. `flow`/`true` are straight (2 points); `false`/`back` are elbow-routed (4 points) so FlowChart can round the corners when drawing. */
  points: Point[];
  label?: string;
}

export interface Lane {
  label: string;
  y: number;
  height: number;
}

export interface FlowChartLayout {
  width: number;
  height: number;
  nodes: LayoutNode[];
  edges: LayoutEdge[];
  lanes: Lane[];
}

export interface ChartFlow {
  setup?: Step[];
  steps: Step[];
  teardown?: Step[];
}

// ---- pass 1: measure (position-independent) ----

interface Measured {
  step: Step;
  hasWhen: boolean;
  whenHeight: number;
  /** Height of the main node: NODE_H for a call, the container's full height for a block. */
  mainHeight: number;
  totalHeight: number;
  width: number;
  nested?: Measured[];
}

function measureStep(step: Step): Measured {
  const hasWhen = !!step.when;
  const whenHeight = hasWhen ? DIAMOND + V_GAP : 0;
  if (isBlockStep(step)) {
    const nested = (step.steps || []).map(measureStep);
    const contentHeight = stackHeight(nested);
    const nestedMaxWidth = nested.reduce((w, m) => Math.max(w, m.width), NODE_W);
    const width = nestedMaxWidth + CONTAINER_PAD_SIDE * 2;
    const mainHeight = CONTAINER_HEADER + CONTAINER_PAD_TOP + contentHeight + CONTAINER_PAD_BOTTOM;
    return { step, hasWhen, whenHeight, mainHeight, totalHeight: whenHeight + mainHeight, width, nested };
  }
  return { step, hasWhen, whenHeight, mainHeight: NODE_H, totalHeight: whenHeight + NODE_H, width: NODE_W };
}

function stackHeight(list: Measured[]): number {
  if (list.length === 0) return 0;
  return list.reduce((sum, m) => sum + m.totalHeight, 0) + V_GAP * (list.length - 1);
}

function maxWidthOf(list: Measured[]): number {
  return list.reduce((w, m) => Math.max(w, m.width), NODE_W);
}

// ---- pass 2: place (turns measurements into absolute coordinates) ----

interface Slot {
  topNode: LayoutNode;
  mainNode: LayoutNode;
  hasWhen: boolean;
}

interface PlaceResult {
  entryNode?: LayoutNode;
  lastMainNode?: LayoutNode;
  contentBottomY: number;
}

interface Out {
  nodes: LayoutNode[];
  edges: LayoutEdge[];
}

function place(list: Measured[], centerX: number, startY: number, phase: Phase, parentContainer: string | undefined, out: Out): PlaceResult {
  const slots: Slot[] = [];
  let y = startY;

  for (let i = 0; i < list.length; i++) {
    if (i > 0) y += V_GAP;
    const m = list[i];
    const step = m.step;

    let topNode: LayoutNode | undefined;
    if (m.hasWhen) {
      const diamond: LayoutNode = {
        id: `${step.id}::when`,
        stepId: step.id,
        kind: 'when',
        x: centerX - DIAMOND / 2,
        y,
        width: DIAMOND,
        height: DIAMOND,
        label: step.id,
        conditionText: truncateExprLocal(step.when!),
        conditionFull: step.when!,
        phase,
        parentContainer,
      };
      out.nodes.push(diamond);
      topNode = diamond;
      y += DIAMOND + V_GAP;
    }

    let mainNode: LayoutNode;
    if (m.nested) {
      const width = m.width;
      const header = blockHeaderText(step);
      const container: LayoutNode = {
        id: `${step.id}::block`,
        stepId: step.id,
        kind: 'block',
        x: centerX - width / 2,
        y,
        width,
        height: m.mainHeight,
        label: step.id,
        conditionText: header.short,
        conditionFull: header.full,
        phase,
        parentContainer,
        breakWhen: step.break_when,
      };
      out.nodes.push(container);
      if (!topNode) topNode = container;
      mainNode = container;

      const nestedStartY = y + CONTAINER_HEADER + CONTAINER_PAD_TOP;
      const inner = place(m.nested, centerX, nestedStartY, phase, step.id, out);
      if (inner.entryNode) {
        out.edges.push({
          id: `${container.id}->${inner.entryNode.id}`,
          kind: 'flow',
          from: container.id,
          to: inner.entryNode.id,
          stepId: step.id,
          points: [
            { x: centerX, y: y + CONTAINER_HEADER },
            { x: centerX, y: inner.entryNode.y },
          ],
        });
      }
      if (inner.entryNode && inner.lastMainNode) {
        out.edges.push({
          id: `${step.id}::back`,
          kind: 'back',
          from: inner.lastMainNode.id,
          to: inner.entryNode.id,
          stepId: step.id,
          points: backEdgePoints(inner.lastMainNode, inner.entryNode, container),
          // No label here: the container's footer already carries
          // break_when, and a second copy on the back edge sat on top of it.
        });
      }

      y += m.mainHeight;
    } else {
      const call: LayoutNode = {
        id: `${step.id}::call`,
        stepId: step.id,
        kind: 'call',
        x: centerX - NODE_W / 2,
        y,
        width: NODE_W,
        height: NODE_H,
        label: step.id,
        sublabel: step.call || (step.example ? `example: ${step.example}` : undefined),
        phase,
        parentContainer,
      };
      out.nodes.push(call);
      if (!topNode) topNode = call;
      mainNode = call;
      y += NODE_H;
    }

    slots.push({ topNode, mainNode, hasWhen: m.hasWhen });
  }

  const contentBottomY = y;

  for (let i = 0; i < slots.length; i++) {
    if (i > 0) {
      out.edges.push({
        id: `${slots[i - 1].mainNode.id}->${slots[i].topNode.id}`,
        kind: 'flow',
        from: slots[i - 1].mainNode.id,
        to: slots[i].topNode.id,
        stepId: slots[i].topNode.stepId,
        points: [bottomCenter(slots[i - 1].mainNode), topCenter(slots[i].topNode)],
      });
    }
    if (slots[i].hasWhen) {
      out.edges.push({
        id: `${slots[i].topNode.id}->${slots[i].mainNode.id}`,
        kind: 'true',
        from: slots[i].topNode.id,
        to: slots[i].mainNode.id,
        stepId: slots[i].topNode.stepId,
        points: [bottomCenter(slots[i].topNode), topCenter(slots[i].mainNode)],
        label: 'true',
      });
    }
  }

  for (let i = 0; i < slots.length; i++) {
    if (!slots[i].hasWhen) continue;
    const diamond = slots[i].topNode;
    const next = i + 1 < slots.length ? slots[i + 1].topNode : undefined;
    const targetPoint = next ? topCenter(next) : { x: centerX, y: contentBottomY };
    out.edges.push({
      id: `${diamond.id}->bypass`,
      kind: 'false',
      from: diamond.id,
      to: next?.id,
      stepId: diamond.stepId,
      points: bypassEdgePoints(diamond, targetPoint),
      label: 'false',
    });
  }

  return { entryNode: slots[0]?.topNode, lastMainNode: slots[slots.length - 1]?.mainNode, contentBottomY };
}

function bottomCenter(n: LayoutNode): Point {
  return { x: n.x + n.width / 2, y: n.y + n.height };
}
function topCenter(n: LayoutNode): Point {
  return { x: n.x + n.width / 2, y: n.y };
}
function leftMid(n: LayoutNode): Point {
  return { x: n.x, y: n.y + n.height / 2 };
}

const BYPASS_BULGE = 40;

function bypassEdgePoints(diamond: LayoutNode, target: Point): Point[] {
  const start = { x: diamond.x + diamond.width, y: diamond.y + diamond.height / 2 };
  const railX = start.x + BYPASS_BULGE;
  return [start, { x: railX, y: start.y }, { x: railX, y: target.y }, target];
}

function backEdgePoints(lastNode: LayoutNode, entryNode: LayoutNode, container: LayoutNode): Point[] {
  const start = leftMid(lastNode);
  const end = leftMid(entryNode);
  const railX = Math.max(container.x + 6, Math.min(start.x, end.x) - (CONTAINER_PAD_SIDE - 10));
  return [start, { x: railX, y: start.y }, { x: railX, y: end.y }, end];
}

function truncateExprLocal(text: string): string {
  const max = 40;
  return text.length <= max ? text : `${text.slice(0, max - 1)}…`;
}

// ---- top-level entry point ----

/** Lays out one lane (setup/steps/teardown) if it has steps, advancing `y`. */
function layoutLane(steps: Step[] | undefined, label: string, phase: Phase, centerX: number, y: number, out: Out, lanes: Lane[]): number {
  if (!steps || steps.length === 0) return y;
  if (label) {
    lanes.push({ label, y, height: LANE_LABEL_HEIGHT });
    y += LANE_LABEL_HEIGHT;
  }
  const measured = steps.map(measureStep);
  const result = place(measured, centerX, y, phase, undefined, out);
  return result.contentBottomY + LANE_GAP;
}

export function layoutFlow(flow: ChartFlow): FlowChartLayout {
  const setupMeasured = (flow.setup || []).map(measureStep);
  const stepsMeasured = (flow.steps || []).map(measureStep);
  const teardownMeasured = (flow.teardown || []).map(measureStep);
  const maxWidth = Math.max(maxWidthOf(setupMeasured), maxWidthOf(stepsMeasured), maxWidthOf(teardownMeasured), NODE_W);
  const centerX = MARGIN + maxWidth / 2;

  const out: Out = { nodes: [], edges: [] };
  const lanes: Lane[] = [];
  let y = MARGIN;

  y = layoutLane(flow.setup, flow.setup && flow.setup.length > 0 ? 'setup' : '', 'setup', centerX, y, out, lanes);
  y = layoutLane(flow.steps, '', 'steps', centerX, y, out, lanes);
  y = layoutLane(flow.teardown, flow.teardown && flow.teardown.length > 0 ? 'teardown' : '', 'teardown', centerX, y, out, lanes);

  const height = Math.max(y - LANE_GAP + MARGIN, MARGIN * 2);
  const width = maxWidth + MARGIN * 2;

  return { width, height, nodes: out.nodes, edges: out.edges, lanes };
}
