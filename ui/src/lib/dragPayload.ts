// Cross-component drag payload for the Flows/Memories/Examples row drag
// (PLAN §34f item 6 follow-up, item 3): Table's row drag source and
// FolderSidebar's drop targets live in different parts of the React tree,
// so they agree on a wire format via a custom MIME type on the native
// HTML5 DataTransfer rather than importing from each other.
const MIME = 'application/x-sapien-move-ids';

export function setDragIds(e: { dataTransfer: DataTransfer }, ids: readonly string[]): void {
  e.dataTransfer.setData(MIME, JSON.stringify(ids));
  e.dataTransfer.effectAllowed = 'move';
}

/** Whether the drag currently under way carries our payload -- checkable
 *  from `dragenter`/`dragover` (unlike `getData`, `types` is readable
 *  before drop in every browser), so a drop target can highlight itself
 *  only for a drag it actually understands. */
export function hasDragIds(e: { dataTransfer: DataTransfer }): boolean {
  return Array.from(e.dataTransfer.types || []).includes(MIME);
}

/** Only reliable at `drop` -- `getData` returns "" during `dragenter`/
 *  `dragover` in most browsers. */
export function getDragIds(e: { dataTransfer: DataTransfer }): string[] {
  try {
    const raw = e.dataTransfer.getData(MIME);
    if (!raw) return [];
    const parsed = JSON.parse(raw);
    return Array.isArray(parsed) ? parsed.filter((x): x is string => typeof x === 'string') : [];
  } catch {
    return [];
  }
}
