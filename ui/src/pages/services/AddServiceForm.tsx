// The "Add service" form (POST /v1/services). Mirrors `sapien service add`
// (internal/cli/service.go): one "path or git url" field whose shape decides
// Source.type, plus name/ref/subdir/contract. Shows the warnings the server
// returns with the created service, same as the CLI's --show-accepted-style
// summary but for the (always shown) unaccepted warnings.
import { useState } from 'react';
import { ApiClientError, services } from '../../api/client';
import type { LintWarning, Service, Source, SourceKind } from '../../api/types';

// Mirrors internal/gitsrc.IsGitURL: a URL scheme sapien recognizes for git
// sources, or the scp-like "user@host:path" remote syntax.
const gitURLSchemes = ['http://', 'https://', 'ssh://', 'git://', 'file://'];
const scpLikeGitURL = /^[A-Za-z0-9_.-]+@[A-Za-z0-9_.-]+:/;

function looksLikeGitURL(loc: string): boolean {
  if (!loc) return false;
  const lower = loc.toLowerCase();
  if (gitURLSchemes.some((s) => lower.startsWith(s))) return true;
  return scpLikeGitURL.test(loc);
}

export function AddServiceForm({ onAdded }: { onAdded: (service: Service) => void }) {
  const [open, setOpen] = useState(false);
  const [location, setLocation] = useState('');
  const [name, setName] = useState('');
  const [ref, setRef] = useState('');
  const [subdir, setSubdir] = useState('');
  const [contract, setContract] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [warnings, setWarnings] = useState<LintWarning[] | null>(null);

  const isGit = looksLikeGitURL(location.trim());

  const reset = () => {
    setLocation('');
    setName('');
    setRef('');
    setSubdir('');
    setContract('');
    setErr(null);
  };

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    const loc = location.trim();
    if (!loc) {
      setErr('Path or git URL is required.');
      return;
    }
    const kind: SourceKind = isGit ? 'git' : 'local';
    const source: Source = { type: kind, contract: contract.trim() || undefined };
    if (kind === 'git') {
      source.url = loc;
      source.ref = ref.trim() || undefined;
      source.subdir = subdir.trim() || undefined;
    } else {
      source.path = loc;
    }

    setSubmitting(true);
    setErr(null);
    setWarnings(null);
    try {
      const svc = await services.add({ name: name.trim(), source });
      setWarnings(svc.warnings || []);
      onAdded(svc);
      if (!svc.warnings || svc.warnings.length === 0) {
        reset();
        setOpen(false);
      }
    } catch (e) {
      setErr(e instanceof ApiClientError ? e.message : String(e));
    } finally {
      setSubmitting(false);
    }
  };

  if (!open) {
    return (
      <button
        type="button"
        onClick={() => setOpen(true)}
        className="rounded border border-slate-300 px-3 py-1.5 text-sm dark:border-slate-700"
      >
        Add service
      </button>
    );
  }

  return (
    <div className="mb-4 rounded border border-slate-200 p-3 dark:border-slate-800">
      <form onSubmit={submit} className="flex flex-col gap-2">
        <div className="flex flex-wrap gap-2">
          <input
            value={location}
            onChange={(e) => setLocation(e.target.value)}
            placeholder="local path or git URL"
            className="w-72 rounded border border-slate-300 bg-white px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-900"
          />
          <input
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="name (optional)"
            className="w-40 rounded border border-slate-300 bg-white px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-900"
          />
          <input
            value={contract}
            onChange={(e) => setContract(e.target.value)}
            placeholder="contract file (optional)"
            className="w-48 rounded border border-slate-300 bg-white px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-900"
          />
        </div>
        {isGit && (
          <div className="flex flex-wrap gap-2">
            <input
              value={ref}
              onChange={(e) => setRef(e.target.value)}
              placeholder="ref: branch, tag, or commit (optional)"
              className="w-64 rounded border border-slate-300 bg-white px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-900"
            />
            <input
              value={subdir}
              onChange={(e) => setSubdir(e.target.value)}
              placeholder="subdir, default &quot;api&quot; (optional)"
              className="w-56 rounded border border-slate-300 bg-white px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-900"
            />
          </div>
        )}
        <p className="text-xs text-slate-400">
          {location.trim() ? (isGit ? 'Detected as a git source.' : 'Detected as a local path.') : 'Paste a local path or a git URL (https://, ssh://, or user@host:path).'}
        </p>
        <div className="flex items-center gap-2">
          <button
            type="submit"
            disabled={submitting}
            className="rounded border border-slate-300 px-3 py-1 text-sm disabled:opacity-50 dark:border-slate-700"
          >
            {submitting ? 'Adding…' : 'Add'}
          </button>
          <button
            type="button"
            onClick={() => {
              reset();
              setWarnings(null);
              setOpen(false);
            }}
            className="rounded border border-slate-300 px-3 py-1 text-sm dark:border-slate-700"
          >
            Cancel
          </button>
        </div>
        {err && <div className="text-sm text-red-600">{err}</div>}
        {warnings && warnings.length > 0 && (
          <div className="mt-1 rounded border border-amber-300 bg-amber-50 p-2 text-sm text-amber-800 dark:border-amber-900 dark:bg-amber-950 dark:text-amber-300">
            <div className="mb-1 font-medium">Service added with {warnings.length} warning{warnings.length === 1 ? '' : 's'}:</div>
            <ul className="list-inside list-disc">
              {warnings.map((w, i) => (
                <li key={i}>
                  <span className="font-mono text-xs">{w.code}</span> {w.message}
                  {w.source && (
                    <span className="text-xs text-amber-600 dark:text-amber-400">
                      {' '}
                      ({w.source.file}
                      {w.source.line ? `:${w.source.line}` : ''})
                    </span>
                  )}
                </li>
              ))}
            </ul>
          </div>
        )}
      </form>
    </div>
  );
}
