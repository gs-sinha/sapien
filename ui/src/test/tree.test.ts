import { describe, expect, it } from 'vitest';
import { buildTree, countLeaves, flattenVisible, folderCheckState, leafKeys } from '../components/tree/buildTree';

interface File {
  path: string;
}

function file(path: string): File {
  return { path };
}

describe('buildTree', () => {
  it('turns flat paths into nested folder/file nodes', () => {
    const tree = buildTree([file('flows/a.yaml'), file('flows/b.yaml'), file('memories/c.md')], (f) => f.path);

    expect(tree.map((n) => n.name)).toEqual(['flows', 'memories']);
    const flows = tree[0];
    expect(flows.isFolder).toBe(true);
    expect(flows.depth).toBe(0);
    expect(flows.children.map((c) => c.name)).toEqual(['a.yaml', 'b.yaml']);
    expect(flows.children[0].isFolder).toBe(false);
    expect(flows.children[0].depth).toBe(1);
    expect(flows.children[0].item).toEqual(file('flows/a.yaml'));
  });

  it('orders folders before files, alphabetically within each group, at every level', () => {
    const tree = buildTree(
      [file('z.yaml'), file('a.yaml'), file('nested/inner.yaml'), file('bravo/x.yaml'), file('alpha/y.yaml')],
      (f) => f.path,
    );
    // Folders (alpha, bravo, nested) sort before files (a.yaml, z.yaml).
    expect(tree.map((n) => n.name)).toEqual(['alpha', 'bravo', 'nested', 'a.yaml', 'z.yaml']);
  });

  it('does not compact a single-child folder chain into one node', () => {
    const tree = buildTree([file('a/b/c/leaf.yaml')], (f) => f.path);
    expect(tree).toHaveLength(1);
    expect(tree[0].name).toBe('a');
    expect(tree[0].children).toHaveLength(1);
    expect(tree[0].children[0].name).toBe('b');
    expect(tree[0].children[0].children).toHaveLength(1);
    expect(tree[0].children[0].children[0].name).toBe('c');
    expect(tree[0].children[0].children[0].children).toHaveLength(1);
    expect(tree[0].children[0].children[0].children[0].name).toBe('leaf.yaml');
    expect(tree[0].children[0].children[0].children[0].isFolder).toBe(false);
  });

  it('shares one folder node between siblings under it', () => {
    const tree = buildTree([file('flows/a.yaml'), file('flows/sub/b.yaml')], (f) => f.path);
    expect(tree).toHaveLength(1);
    const flows = tree[0];
    expect(flows.children.map((c) => c.name)).toEqual(['sub', 'a.yaml']);
    expect(flows.children[0].key).toBe('flows/sub');
  });

  it('gives every node a stable, full "/"-joined key', () => {
    const tree = buildTree([file('a/b/leaf.yaml')], (f) => f.path);
    expect(tree[0].key).toBe('a');
    expect(tree[0].children[0].key).toBe('a/b');
    expect(tree[0].children[0].children[0].key).toBe('a/b/leaf.yaml');
  });

  it('ignores an empty path', () => {
    expect(buildTree([file('')], (f) => f.path)).toEqual([]);
  });

  it('returns an empty array for an empty item list', () => {
    expect(buildTree([], (f: File) => f.path)).toEqual([]);
  });
});

describe('flattenVisible', () => {
  const tree = buildTree([file('a/x.yaml'), file('a/b/y.yaml'), file('c.yaml')], (f) => f.path);

  it('lists every row when everything is expanded', () => {
    const rows = flattenVisible(tree, () => true);
    expect(rows.map((n) => n.key)).toEqual(['a', 'a/b', 'a/b/y.yaml', 'a/x.yaml', 'c.yaml']);
  });

  it('skips a collapsed folder\'s descendants but keeps the folder row itself', () => {
    const rows = flattenVisible(tree, (key) => key !== 'a');
    expect(rows.map((n) => n.key)).toEqual(['a', 'c.yaml']);
  });

  it('hides leaf (file) rows entirely when showLeaves is false, keeping folders', () => {
    const rows = flattenVisible(tree, () => true, false);
    expect(rows.map((n) => n.key)).toEqual(['a', 'a/b']);
  });
});

describe('leafKeys / countLeaves', () => {
  const tree = buildTree([file('a/x.yaml'), file('a/b/y.yaml'), file('a/b/z.yaml'), file('c.yaml')], (f) => f.path);
  const folderA = tree.find((n) => n.name === 'a')!;

  it('lists every leaf under a folder, recursively', () => {
    expect(leafKeys(folderA).sort()).toEqual(['a/b/y.yaml', 'a/b/z.yaml', 'a/x.yaml'].sort());
  });

  it('returns just itself for a leaf node', () => {
    const leaf = folderA.children.find((n) => n.name === 'x.yaml')!;
    expect(leafKeys(leaf)).toEqual(['a/x.yaml']);
  });

  it('counts leaves at or under a folder', () => {
    expect(countLeaves(folderA)).toBe(3);
    const folderB = folderA.children.find((n) => n.name === 'b')!;
    expect(countLeaves(folderB)).toBe(2);
  });

  it('narrows by an `include` predicate', () => {
    const onlyY = (n: { key: string }) => n.key === 'a/b/y.yaml';
    expect(leafKeys(folderA, onlyY)).toEqual(['a/b/y.yaml']);
    expect(countLeaves(folderA, onlyY)).toBe(1);
  });
});

describe('folderCheckState (tri-state)', () => {
  const tree = buildTree([file('a/x.yaml'), file('a/y.yaml'), file('a/b/z.yaml'), file('c.yaml')], (f) => f.path);
  const folderA = tree.find((n) => n.name === 'a')!;
  const folderB = folderA.children.find((n) => n.name === 'b')!;

  it('is "unchecked" when nothing under the folder is checked', () => {
    expect(folderCheckState(folderA, new Set())).toBe('unchecked');
  });

  it('is "checked" when every leaf under the folder is checked', () => {
    expect(folderCheckState(folderA, new Set(['a/x.yaml', 'a/y.yaml', 'a/b/z.yaml']))).toBe('checked');
  });

  it('is "indeterminate" when some but not all leaves are checked', () => {
    expect(folderCheckState(folderA, new Set(['a/x.yaml']))).toBe('indeterminate');
  });

  it('a leaf itself is "checked" or "unchecked", never "indeterminate"', () => {
    const leaf = folderA.children.find((n) => n.name === 'x.yaml')!;
    expect(folderCheckState(leaf, new Set(['a/x.yaml']))).toBe('checked');
    expect(folderCheckState(leaf, new Set())).toBe('unchecked');
  });

  it('a nested folder\'s state only looks at its own descendants', () => {
    expect(folderCheckState(folderB, new Set(['a/b/z.yaml']))).toBe('checked');
    expect(folderCheckState(folderB, new Set(['a/x.yaml']))).toBe('unchecked');
  });

  it('checking every leaf bubbles the parent to "checked", not stuck "indeterminate"', () => {
    const checked = new Set(['a/x.yaml', 'a/y.yaml', 'a/b/z.yaml', 'c.yaml']);
    expect(folderCheckState(folderA, checked)).toBe('checked');
  });

  it('excludes leaves the `checkable` predicate rejects from both the total and the checked count', () => {
    // Only a/x.yaml is checkable; a/y.yaml and a/b/z.yaml are excluded
    // entirely (e.g. "unpushed" rows on the Changes page).
    const checkable = (n: { key: string }) => n.key === 'a/x.yaml';
    expect(folderCheckState(folderA, new Set(), checkable)).toBe('unchecked');
    expect(folderCheckState(folderA, new Set(['a/x.yaml']), checkable)).toBe('checked');
  });

  it('is "unchecked" (not indeterminate) for a folder with no checkable leaves at all', () => {
    const noneCheckable = () => false;
    expect(folderCheckState(folderA, new Set(['a/x.yaml']), noneCheckable)).toBe('unchecked');
  });
});
