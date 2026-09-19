// Client-side folder filtering shared by the Flows/Memories/Examples pages
// (PLAN §34f item 6). Items carry `folder` ("" or absent at the root,
// `/`-separated); the daemon supports a `?folder=` prefix query too, but
// these pages already fetch their full list for the text/tier filters, so
// folder narrowing is done here against that same list rather than as a
// second round trip.
export interface Foldered {
  folder?: string;
}

export function folderOf(item: Foldered): string {
  return item.folder || '';
}

/** Every distinct non-root folder value present among `items`. */
export function distinctFolders(items: readonly Foldered[]): string[] {
  const set = new Set<string>();
  for (const it of items) if (it.folder) set.add(it.folder);
  return Array.from(set);
}

/** `items` whose folder is exactly `folder` or nested under it. `null`/""
 *  (no folder selected -- "All") returns every item unfiltered. */
export function underFolder<T extends Foldered>(items: readonly T[], folder: string | null | undefined): T[] {
  if (!folder) return items.slice();
  return items.filter((it) => {
    const f = folderOf(it);
    return f === folder || f.startsWith(folder + '/');
  });
}
