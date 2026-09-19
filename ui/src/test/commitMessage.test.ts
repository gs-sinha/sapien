import { describe, expect, it } from 'vitest';
import { generateCommitMessage } from '../pages/changes/commitMessage';
import type { RepoChangeFile } from '../api/types';

function f(path: string, state: RepoChangeFile['state'], kind: RepoChangeFile['kind'] = 'other'): RepoChangeFile {
  return { path, state, kind };
}

describe('generateCommitMessage', () => {
  it('returns an empty string when nothing is selected', () => {
    expect(generateCommitMessage([f('a', 'untracked', 'flow')], new Set())).toBe('');
  });

  it('matches the contract example: "Add 3 examples and 1 flow; update workspace config"', () => {
    const files: RepoChangeFile[] = [
      f('examples/a.yaml', 'untracked', 'example'),
      f('examples/b.yaml', 'untracked', 'example'),
      f('examples/c.yaml', 'untracked', 'example'),
      f('flows/x.yaml', 'untracked', 'flow'),
      f('sapien.workspace.yaml', 'modified', 'workspace'),
    ];
    const selected = new Set(files.map((x) => x.path));
    expect(generateCommitMessage(files, selected)).toBe('Add 3 examples and 1 flow; update workspace config');
  });

  it('only counts files that are actually selected', () => {
    const files: RepoChangeFile[] = [
      f('flows/a.yaml', 'untracked', 'flow'),
      f('flows/b.yaml', 'untracked', 'flow'),
    ];
    expect(generateCommitMessage(files, new Set(['flows/a.yaml']))).toBe('Add 1 flow');
  });

  it('singular vs plural nouns', () => {
    expect(generateCommitMessage([f('a', 'untracked', 'memory')], new Set(['a']))).toBe('Add 1 memory');
    expect(
      generateCommitMessage([f('a', 'untracked', 'memory'), f('b', 'untracked', 'memory')], new Set(['a', 'b'])),
    ).toBe('Add 2 memories');
  });

  it('groups deletions and renames into their own clauses, capitalizing only the first letter of the whole message', () => {
    const files: RepoChangeFile[] = [f('a', 'deleted', 'flow'), f('b', 'renamed', 'example')];
    expect(generateCommitMessage(files, new Set(['a', 'b']))).toBe('Remove 1 flow; rename 1 example');
  });

  it('treats a conflicted file as an update, alongside modified', () => {
    const files: RepoChangeFile[] = [f('a', 'conflicted', 'flow')];
    expect(generateCommitMessage(files, new Set(['a']))).toBe('Update 1 flow');
  });

  it('files without a kind fall back to "file"/"files"', () => {
    const files: RepoChangeFile[] = [f('.gitignore', 'untracked')];
    expect(generateCommitMessage(files, new Set(['.gitignore']))).toBe('Add 1 file');
  });

  it('capitalizes the first letter of the generated message even when it starts with "update"', () => {
    const files: RepoChangeFile[] = [f('environments/prod.yaml', 'modified', 'environment')];
    expect(generateCommitMessage(files, new Set(['environments/prod.yaml']))).toBe('Update 1 environment');
  });
});
