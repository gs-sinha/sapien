import { useState } from 'react';
import { runs } from '../../api/client';
import { StatusPill } from '../StatusPill';
import { Timestamp } from '../Timestamp';
import { pushToast } from '../../state/toast';
import type { Run } from '../../api/types';

export function SummaryBar({ run, onPinChange }: { run: Run; onPinChange: () => void }) {
  const [pinning, setPinning] = useState(false);

  const togglePin = async () => {
    setPinning(true);
    try {
      await runs.pin(run.id, !run.pinned);
      onPinChange();
    } catch (err) {
      pushToast('error', err instanceof Error ? err.message : 'failed to update pin');
    } finally {
      setPinning(false);
    }
  };

  return (
    <div className="flex flex-wrap items-center gap-x-5 gap-y-2 rounded border border-slate-200 p-3 text-sm dark:border-slate-800">
      <StatusPill status={run.status} />
      <span>
        <span className="text-slate-400">env</span> {run.environment}
      </span>
      <span>
        <span className="text-slate-400">trigger</span> {run.trigger || '-'}
      </span>
      <span>
        <span className="text-slate-400">started</span> <Timestamp value={run.started} />
      </span>
      <span>
        <span className="text-slate-400">duration</span> {run.duration_ms !== undefined ? `${run.duration_ms} ms` : '-'}
      </span>
      <span>
        <span className="text-slate-400">assertions</span> {run.summary.assertions - run.summary.assertions_failed}/{run.summary.assertions}{' '}
        passed
      </span>
      <button
        type="button"
        onClick={togglePin}
        disabled={pinning}
        className={`ml-auto rounded border px-2 py-1 text-xs ${
          run.pinned
            ? 'border-amber-300 bg-amber-100 text-amber-800 dark:border-amber-800 dark:bg-amber-900/40 dark:text-amber-300'
            : 'border-slate-300 text-slate-600 dark:border-slate-700 dark:text-slate-400'
        }`}
      >
        {run.pinned ? 'Pinned' : 'Pin'}
      </button>
    </div>
  );
}
