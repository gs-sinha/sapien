// Picks a saved example for this operation to prefill params/body/headers.
// The parent owns applying the selection (it needs to overwrite several
// pieces of form state at once); this just reports which id was picked.
import type { SavedExample } from '../../api/types';

export function ExamplePicker({
  examples,
  value,
  onChange,
}: {
  examples: SavedExample[];
  value: string;
  onChange: (id: string) => void;
}) {
  if (examples.length === 0) {
    return <div className="text-sm text-slate-400">No saved examples for this operation yet.</div>;
  }

  return (
    <select
      value={value}
      onChange={(e) => onChange(e.target.value)}
      className="w-full rounded border border-slate-300 bg-white px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-900"
    >
      <option value="">(start from a blank form)</option>
      {examples.map((ex) => (
        <option key={ex.id} value={ex.id}>
          {ex.id}
          {ex.verified ? ` -- verified (${ex.verified.env || 'unknown env'})` : ' -- draft'}
        </option>
      ))}
    </select>
  );
}
