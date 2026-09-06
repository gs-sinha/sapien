import { useState } from 'react';
import { flows } from '../../api/client';
import { Table } from '../../components/Table';
import { pushToast } from '../../state/toast';
import type { Diagnostic, ValidationResult } from '../../api/types';

function SeverityBadge({ severity }: { severity: Diagnostic['severity'] }) {
  const cls =
    severity === 'error'
      ? 'bg-red-100 text-red-800 dark:bg-red-900/40 dark:text-red-300'
      : 'bg-amber-100 text-amber-800 dark:bg-amber-900/40 dark:text-amber-300';
  return <span className={`inline-block rounded-full px-2 py-0.5 text-xs font-medium ${cls}`}>{severity}</span>;
}

export function ValidatePanel({ source }: { source: string }) {
  const [result, setResult] = useState<ValidationResult | null>(null);
  const [validating, setValidating] = useState(false);

  const validate = async () => {
    setValidating(true);
    try {
      const res = await flows.validate({ yaml: source });
      setResult(res);
    } catch (err) {
      pushToast('error', err instanceof Error ? err.message : 'validation failed');
    } finally {
      setValidating(false);
    }
  };

  return (
    <div>
      <button
        type="button"
        onClick={validate}
        disabled={validating}
        className="rounded border border-slate-300 px-3 py-1 text-sm disabled:opacity-50 dark:border-slate-700"
      >
        {validating ? 'Validating…' : 'Validate'}
      </button>
      {result && (
        <div className="mt-2">
          {result.valid && (!result.diagnostics || result.diagnostics.length === 0) ? (
            <div className="text-sm text-emerald-700 dark:text-emerald-400">Valid, no issues.</div>
          ) : (
            <Table<Diagnostic>
              rowKey={(d) => `${d.code}-${d.line ?? ''}-${d.step_id || ''}-${d.message}`}
              columns={[
                { key: 'severity', header: 'Severity', render: (d) => <SeverityBadge severity={d.severity} /> },
                { key: 'line', header: 'Line', render: (d) => d.line ?? '-' },
                { key: 'code', header: 'Code', render: (d) => <span className="font-mono text-xs">{d.code}</span> },
                { key: 'message', header: 'Message', render: (d) => d.message },
                { key: 'suggestions', header: 'Suggestions', render: (d) => (d.suggestions || []).join('; ') || '-' },
              ]}
              rows={result.diagnostics || []}
            />
          )}
        </div>
      )}
    </div>
  );
}
