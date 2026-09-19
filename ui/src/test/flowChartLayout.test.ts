import { describe, expect, it } from 'vitest';
import { layoutFlow } from '../components/flowchart/layout';
import type { ChartFlow, LayoutNode } from '../components/flowchart/layout';
import type { Step } from '../api/types';

function call(id: string, extra: Partial<Step> = {}): Step {
  return { id, call: `svc.${id}`, ...extra };
}

function bounds(n: LayoutNode) {
  return { left: n.x, right: n.x + n.width, top: n.y, bottom: n.y + n.height };
}

function overlaps(a: LayoutNode, b: LayoutNode): boolean {
  const ba = bounds(a);
  const bb = bounds(b);
  return ba.left < bb.right && bb.left < ba.right && ba.top < bb.bottom && bb.top < ba.bottom;
}

function encloses(container: LayoutNode, node: LayoutNode): boolean {
  const c = bounds(container);
  const n = bounds(node);
  return n.left >= c.left && n.right <= c.right && n.top >= c.top && n.bottom <= c.bottom;
}

describe('layoutFlow: a plain linear flow', () => {
  const flow: ChartFlow = { steps: [call('a'), call('b'), call('c')] };
  const layout = layoutFlow(flow);

  it('produces one call node per step, in document order', () => {
    expect(layout.nodes.map((n) => n.kind)).toEqual(['call', 'call', 'call']);
    expect(layout.nodes.map((n) => n.stepId)).toEqual(['a', 'b', 'c']);
  });

  it('stacks them top to bottom with no vertical overlap', () => {
    const [a, b, c] = layout.nodes;
    expect(a.y).toBeLessThan(b.y);
    expect(b.y).toBeLessThan(c.y);
    expect(overlaps(a, b)).toBe(false);
    expect(overlaps(b, c)).toBe(false);
  });

  it('connects consecutive steps with a flow edge each', () => {
    expect(layout.edges).toHaveLength(2);
    for (const e of layout.edges) expect(e.kind).toBe('flow');
    expect(layout.edges[0]).toMatchObject({ from: 'a::call', to: 'b::call' });
    expect(layout.edges[1]).toMatchObject({ from: 'b::call', to: 'c::call' });
  });

  it('has no setup/teardown lanes when neither is present', () => {
    expect(layout.lanes).toEqual([]);
  });
});

describe('layoutFlow: a 60-step flow has no overlapping nodes', () => {
  const steps = Array.from({ length: 60 }, (_, i) => call(`s${i}`));
  const layout = layoutFlow({ steps });

  it('lays out all 60 steps', () => {
    expect(layout.nodes).toHaveLength(60);
  });

  it('never overlaps any two nodes', () => {
    for (let i = 0; i < layout.nodes.length; i++) {
      for (let j = i + 1; j < layout.nodes.length; j++) {
        expect(overlaps(layout.nodes[i], layout.nodes[j])).toBe(false);
      }
    }
  });

  it('fits within the reported width/height', () => {
    for (const n of layout.nodes) {
      expect(n.x).toBeGreaterThanOrEqual(0);
      expect(n.y).toBeGreaterThanOrEqual(0);
      expect(n.x + n.width).toBeLessThanOrEqual(layout.width);
      expect(n.y + n.height).toBeLessThanOrEqual(layout.height);
    }
  });
});

describe('layoutFlow: `when` produces a diamond with a true edge and a bypass', () => {
  const flow: ChartFlow = { steps: [call('a'), call('release', { when: 'inputs.releaseNow' }), call('check')] };
  const layout = layoutFlow(flow);

  it('inserts a diamond node in front of the guarded step, same stepId', () => {
    const diamond = layout.nodes.find((n) => n.kind === 'when');
    expect(diamond).toBeDefined();
    expect(diamond!.stepId).toBe('release');
    expect(diamond!.conditionFull).toBe('inputs.releaseNow');
  });

  it('node order keeps the diamond immediately before its guarded call node', () => {
    const ids = layout.nodes.map((n) => n.id);
    const diamondIdx = ids.indexOf('release::when');
    const callIdx = ids.indexOf('release::call');
    expect(diamondIdx).toBeGreaterThanOrEqual(0);
    expect(callIdx).toBe(diamondIdx + 1);
  });

  it('has a true edge from the diamond into the guarded step', () => {
    const trueEdge = layout.edges.find((e) => e.kind === 'true');
    expect(trueEdge).toMatchObject({ from: 'release::when', to: 'release::call', stepId: 'release' });
  });

  it('has a false bypass edge from the diamond to the next node, skipping the guarded step', () => {
    const bypass = layout.edges.find((e) => e.kind === 'false');
    expect(bypass).toBeDefined();
    expect(bypass!.from).toBe('release::when');
    expect(bypass!.to).toBe('check::call');
    // The bypass routes around, not straight through the guarded node: at
    // least 4 points (an elbow), not a straight 2-point line.
    expect(bypass!.points.length).toBeGreaterThanOrEqual(4);
  });

  it('a bypass off the very last step has no `to` node, just an exit point', () => {
    const trailing: ChartFlow = { steps: [call('a'), call('maybe', { when: 'inputs.x' })] };
    const l = layoutFlow(trailing);
    const bypass = l.edges.find((e) => e.kind === 'false');
    expect(bypass!.to).toBeUndefined();
  });
});

describe('layoutFlow: a loop block', () => {
  const block: Step = {
    id: 'create-each',
    call: '',
    foreach: 'inputs.customerIds',
    max: 50,
    steps: [call('create'), call('confirm')],
  };
  const flow: ChartFlow = { steps: [call('before'), block, call('after')] };
  const layout = layoutFlow(flow);

  it('renders the block as a container node holding its nested step', () => {
    const container = layout.nodes.find((n) => n.id === 'create-each::block');
    expect(container).toBeDefined();
    expect(container!.kind).toBe('block');
    expect(container!.conditionFull).toContain('foreach inputs.customerIds');

    const nested = layout.nodes.find((n) => n.id === 'create::call');
    expect(nested).toBeDefined();
    expect(nested!.parentContainer).toBe('create-each');
  });

  it("the container's bounds enclose every nested node", () => {
    const container = layout.nodes.find((n) => n.id === 'create-each::block')!;
    const nested = layout.nodes.filter((n) => n.parentContainer === 'create-each');
    expect(nested.length).toBeGreaterThan(0);
    for (const n of nested) expect(encloses(container, n)).toBe(true);
  });

  it('has a back edge from the last nested node up to the block entry', () => {
    const back = layout.edges.find((e) => e.kind === 'back');
    expect(back).toBeDefined();
    expect(back!.from).toBe('confirm::call');
    expect(back!.to).toBe('create::call');
    expect(back!.stepId).toBe('create-each');
  });

  it('keeps the block in the top-level spine (edges connect around it, not through it)', () => {
    const before = layout.edges.find((e) => e.from === 'before::call' && e.to === 'create-each::block');
    const after = layout.edges.find((e) => e.from === 'create-each::block' && e.to === 'after::call');
    expect(before).toBeDefined();
    expect(after).toBeDefined();
  });

  it('carries a break_when label onto the back edge when set', () => {
    const withBreak: ChartFlow = { steps: [{ ...block, break_when: 'iter.index > 3' }] };
    const l = layoutFlow(withBreak);
    const back = l.edges.find((e) => e.kind === 'back');
    expect(back!.label).toContain('break:');
    expect(back!.label).toContain('iter.index > 3');
  });
});

describe('layoutFlow: a `when` nested inside a loop block gets its own diamond', () => {
  const block: Step = {
    id: 'each',
    call: '',
    foreach: 'inputs.ids',
    steps: [call('maybe-skip', { when: 'iter.item.active' }), call('always')],
  };
  const layout = layoutFlow({ steps: [block] });

  it('renders a diamond for the nested when, parented to the block', () => {
    const diamond = layout.nodes.find((n) => n.id === 'maybe-skip::when');
    expect(diamond).toBeDefined();
    expect(diamond!.parentContainer).toBe('each');
    expect(diamond!.conditionFull).toBe('iter.item.active');
  });

  it('the nested bypass still targets the next nested node, not something outside the block', () => {
    const bypass = layout.edges.find((e) => e.kind === 'false' && e.stepId === 'maybe-skip');
    expect(bypass!.to).toBe('always::call');
  });
});

describe('layoutFlow: setup/teardown lanes', () => {
  const flow: ChartFlow = {
    setup: [call('provision')],
    steps: [call('main')],
    teardown: [call('cleanup')],
  };
  const layout = layoutFlow(flow);

  it('labels a setup and a teardown lane, but not the main steps', () => {
    expect(layout.lanes.map((l) => l.label)).toEqual(['setup', 'teardown']);
  });

  it('orders nodes setup, then steps, then teardown', () => {
    expect(layout.nodes.map((n) => n.stepId)).toEqual(['provision', 'main', 'cleanup']);
  });

  it('places the setup lane above the steps lane, and steps above teardown', () => {
    const [provision, main, cleanup] = layout.nodes;
    expect(provision.y).toBeLessThan(main.y);
    expect(main.y).toBeLessThan(cleanup.y);
  });

  it('omits a lane entirely when there is nothing in it', () => {
    const l = layoutFlow({ steps: [call('only')] });
    expect(l.lanes).toEqual([]);
  });
});
