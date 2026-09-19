import { describe, expect, it } from 'vitest';
import { generateCommitMessage } from '../pages/changes/commitMessage';
import type { RepoChangeFile } from '../api/types';

describe('generateCommitMessage: moves', () => {
  it('reads a deleted file and an added one of the same name and kind as one move', () => {
    const files: RepoChangeFile[] = [
      { path: 'flows/a.flow.yaml', state: 'deleted', kind: 'flow' },
      { path: 'flows/billing/a.flow.yaml', state: 'untracked', kind: 'flow', id: 'a' },
      { path: 'flows/b.flow.yaml', state: 'deleted', kind: 'flow' },
      { path: 'flows/billing/b.flow.yaml', state: 'untracked', kind: 'flow', id: 'b' },
      { path: 'flows/new.flow.yaml', state: 'untracked', kind: 'flow', id: 'new' },
    ];
    const all = new Set(files.map((f) => f.path));
    expect(generateCommitMessage(files, all)).toBe('Add 1 flow; move 2 flows');
  });

  it('leaves a deletion alone when its new home is not selected', () => {
    const files: RepoChangeFile[] = [
      { path: 'flows/a.flow.yaml', state: 'deleted', kind: 'flow' },
      { path: 'flows/billing/a.flow.yaml', state: 'untracked', kind: 'flow', id: 'a' },
    ];
    expect(generateCommitMessage(files, new Set(['flows/a.flow.yaml']))).toBe('Remove 1 flow');
  });
});
