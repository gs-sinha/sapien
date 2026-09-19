import { describe, expect, it } from 'vitest';
import { distinctFolders, folderOf, underFolder } from '../lib/folders';

describe('folderOf', () => {
  it('is "" for an absent or empty folder', () => {
    expect(folderOf({})).toBe('');
    expect(folderOf({ folder: '' })).toBe('');
  });
  it('returns the folder value otherwise', () => {
    expect(folderOf({ folder: 'a/b' })).toBe('a/b');
  });
});

describe('distinctFolders', () => {
  it('lists every distinct non-root folder', () => {
    expect(distinctFolders([{ folder: 'a' }, { folder: 'a' }, { folder: 'b/c' }, {}])).toEqual(['a', 'b/c']);
  });

  it('is empty when nothing has a folder', () => {
    expect(distinctFolders([{}, { folder: '' }])).toEqual([]);
  });
});

describe('underFolder', () => {
  const items = [{ id: 1, folder: '' }, { id: 2, folder: 'a' }, { id: 3, folder: 'a/b' }, { id: 4, folder: 'ab' }];

  it('returns every item, unfiltered, for null/undefined/""', () => {
    expect(underFolder(items, null)).toHaveLength(4);
    expect(underFolder(items, undefined)).toHaveLength(4);
    expect(underFolder(items, '')).toHaveLength(4);
  });

  it('matches the folder itself and everything nested below it', () => {
    const result = underFolder(items, 'a');
    expect(result.map((i) => i.id)).toEqual([2, 3]);
  });

  it('does not treat "ab" as nested under "a" (prefix boundary is the "/")', () => {
    const result = underFolder(items, 'a');
    expect(result.some((i) => i.id === 4)).toBe(false);
  });

  it('an exact leaf folder match with nothing nested returns just that item', () => {
    expect(underFolder(items, 'a/b').map((i) => i.id)).toEqual([3]);
  });
});
