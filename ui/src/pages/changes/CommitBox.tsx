// Commit box pinned under the Changes page's tree: message textarea, Commit,
// then Pull/Push with the same enable/disable rules as StatusBar's
// RepoSegment (components/StatusBar.tsx) -- duplicated here in miniature
// rather than extracted, since StatusBar is a shared hot-spot file another
// agent is also editing this round (see the build report).
import { useState } from 'react';
import { repo as repoApi } from '../../api/client';
import { PushButton } from '../../components/tiers';
import { useRepo } from '../../state/repo';
import { pushToast } from '../../state/toast';
import type { RepoStatus } from '../../api/types';
import { plural } from '../../lib/plural';

export function CommitBox({
  status,
  selectedCount,
  message,
  onMessageChange,
  onCommit,
  committing,
  commitError,
}: {
  status: RepoStatus;
  selectedCount: number;
  message: string;
  onMessageChange: (v: string) => void;
  onCommit: () => void;
  committing: boolean;
  commitError: string | null;
}) {
  const [pullBusy, setPullBusy] = useState(false);

  const pull = async () => {
    setPullBusy(true);
    try {
      const next = await repoApi.pull();
      useRepo.getState().setStatus(next);
      pushToast('success', `pulled ${plural(next.pulled_count ?? 0, 'commit')}`);
    } catch (e) {
      pushToast('error', e instanceof Error ? e.message : 'Pull failed.');
    } finally {
      setPullBusy(false);
    }
  };

  return (
    <div className="border-t border-slate-200 p-3 dark:border-slate-800">
      <textarea
        value={message}
        onChange={(e) => onMessageChange(e.target.value)}
        rows={3}
        placeholder="Commit message"
        aria-label="Commit message"
        className="w-full rounded border border-slate-300 bg-white p-2 text-sm dark:border-slate-700 dark:bg-slate-900"
      />
      {commitError && <div className="mt-1 text-xs text-red-600">{commitError}</div>}
      <div className="mt-2 flex flex-wrap items-center gap-2">
        <button
          type="button"
          disabled={selectedCount === 0 || message.trim() === '' || committing}
          onClick={onCommit}
          className="rounded border border-sky-600 bg-sky-50 px-2 py-1 text-xs text-sky-800 disabled:opacity-50 dark:border-sky-500 dark:bg-sky-950 dark:text-sky-300"
        >
          {committing ? 'Committing…' : `Commit ${selectedCount} files`}
        </button>
        {status.behind > 0 && status.dirty === 0 && (
          <button
            type="button"
            disabled={pullBusy}
            onClick={pull}
            className="rounded border border-slate-300 px-2 py-1 text-xs disabled:opacity-50 dark:border-slate-700"
          >
            {pullBusy ? 'Pulling…' : 'Pull'}
          </button>
        )}
        {status.behind > 0 && status.dirty > 0 && (
          <span className="text-xs text-amber-600 dark:text-amber-400">pull blocked: {status.dirty} uncommitted</span>
        )}
        {status.ahead > 0 && status.behind === 0 && <PushButton />}
        {status.ahead > 0 && status.behind > 0 && <span className="text-xs text-amber-600 dark:text-amber-400">pull first</span>}
      </div>
    </div>
  );
}
