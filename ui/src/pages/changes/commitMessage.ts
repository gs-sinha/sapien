// Generates the Changes page's prefilled commit message from the current
// selection: "Add 3 examples and 1 flow; update workspace config" -- grouped
// by kind within each of add/update/remove/rename, so a commit spanning
// several kinds and actions still reads as one sentence. ChangesPage never
// overwrites the textarea with this once the user has typed into it (see
// the `messageEdited` flag there).
import type { RepoChangeFile, RepoItemKind } from '../../api/types';

type Bucket = 'add' | 'update' | 'remove' | 'rename';

const VERB: Record<Bucket, string> = { add: 'Add', update: 'update', remove: 'remove', rename: 'rename' };

// [singular, plural]; "workspace" has effectively one file (sapien.workspace.yaml)
// so it always reads as the bare phrase, never "1 workspace configs".
const NOUN: Record<RepoItemKind, [string, string]> = {
  flow: ['flow', 'flows'],
  memory: ['memory', 'memories'],
  example: ['example', 'examples'],
  environment: ['environment', 'environments'],
  workspace: ['workspace config', 'workspace config'],
  other: ['file', 'files'],
};

function nounFor(kind: RepoItemKind, n: number): string {
  return NOUN[kind][n === 1 ? 0 : 1];
}

function bucketOf(state: RepoChangeFile['state']): Bucket {
  if (state === 'untracked') return 'add';
  if (state === 'deleted') return 'remove';
  if (state === 'renamed') return 'rename';
  return 'update'; // modified, conflicted
}

function clauseFor(bucket: Bucket, counts: Map<RepoItemKind, number>): string | null {
  const parts = Array.from(counts.entries())
    .filter(([, n]) => n > 0)
    .sort((a, b) => a[0].localeCompare(b[0]))
    .map(([kind, n]) => (kind === 'workspace' ? nounFor(kind, n) : `${n} ${nounFor(kind, n)}`));
  if (parts.length === 0) return null;
  const joined = parts.length === 1 ? parts[0] : parts.slice(0, -1).join(', ') + ' and ' + parts[parts.length - 1];
  return `${VERB[bucket]} ${joined}`;
}

export function generateCommitMessage(files: readonly RepoChangeFile[], selected: ReadonlySet<string>): string {
  const sel = files.filter((f) => selected.has(f.path));
  if (sel.length === 0) return '';

  const buckets: Record<Bucket, Map<RepoItemKind, number>> = {
    add: new Map(),
    update: new Map(),
    remove: new Map(),
    rename: new Map(),
  };
  for (const f of sel) {
    const kind = f.kind || 'other';
    const bucket = bucketOf(f.state);
    buckets[bucket].set(kind, (buckets[bucket].get(kind) || 0) + 1);
  }

  const clauses = (['add', 'update', 'remove', 'rename'] as const)
    .map((b) => clauseFor(b, buckets[b]))
    .filter((c): c is string => !!c);
  if (clauses.length === 0) return '';
  const msg = clauses.join('; ');
  return msg.charAt(0).toUpperCase() + msg.slice(1);
}
