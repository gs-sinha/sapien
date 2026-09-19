// Keeps the Flows/Memories/Examples folder-sidebar selection in the `?folder=`
// query param, so it survives a reload and responds to back/forward, per
// PLAN §34f item 6. Absent (or empty) means "All" -- no folder filter.
import { useCallback } from 'react';
import { useSearchParams } from 'react-router-dom';

export function useFolderParam(): [string | null, (folder: string | null) => void] {
  const [searchParams, setSearchParams] = useSearchParams();
  const folder = searchParams.get('folder') || null;

  const setFolder = useCallback(
    (next: string | null) => {
      setSearchParams((prev) => {
        const copy = new URLSearchParams(prev);
        if (next) copy.set('folder', next);
        else copy.delete('folder');
        return copy;
      });
    },
    [setSearchParams],
  );

  return [folder, setFolder];
}
