// The "Add service" form (POST /v1/services). Mirrors `sapien service add`
// (internal/cli/service.go): one "path or git url" field whose shape decides
// Source.type, plus name/ref/subdir/contract. Shows the warnings the server
// returns with the created service, same as the CLI's --show-accepted-style
// summary but for the (always shown) unaccepted warnings.
import { useState } from 'react';
import { ApiClientError, services } from '../../api/client';
import type { LintWarning, Service } from '../../api/types';

// A subdirectory-of-a-repository refusal and a "not a git checkout with an
// origin at all" refusal both carry details.local_add: true (the daemon's
// signal that a plain local Add is the sensible fallback); only the former
// can be retried with allow_subdir. There's no separate flag for that on the
// wire yet, so it's read out of the message the daemon already composed.
function looksLikeSubdirIssue(message: string): boolean {
  return /subdirector|not its root|inside a repository/i.test(message);
}

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

// A daemon refusal that carries details.local_add: true, plus which follow-up
// action(s) to offer instead of a plain toast: always "add as local path",
// and "commit repository with subdir" only when the message names a
// subdirectory-of-a-repository refusal rather than a checkout with no origin
// at all.
interface CheckoutIssue {
  message: string;
  path: string;
  offerSubdirRetry: boolean;
}

export function AddServiceForm({ onAdded }: { onAdded: (service: Service) => void }) {
  const [open, setOpen] = useState(false);
  const [location, setLocation] = useState('');
  const [name, setName] = useState('');
  const [ref, setRef] = useState('');
  const [subdir, setSubdir] = useState('');
  const [contract, setContract] = useState('');
  const [asTeamSource, setAsTeamSource] = useState(true);
  const [submitting, setSubmitting] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [warnings, setWarnings] = useState<LintWarning[] | null>(null);
  const [checkoutIssue, setCheckoutIssue] = useState<CheckoutIssue | null>(null);

  const isGit = looksLikeGitURL(location.trim());

  const reset = () => {
    setLocation('');
    setName('');
    setRef('');
    setSubdir('');
    setContract('');
    setAsTeamSource(true);
    setErr(null);
    setCheckoutIssue(null);
  };

  const addLocalService = (loc: string): Promise<Service> =>
    services.add({ name: name.trim(), source: { type: 'local', path: loc, contract: contract.trim() || undefined } });

  const addGitService = (loc: string): Promise<Service> =>
    services.add({
      name: name.trim(),
      source: { type: 'git', url: loc, ref: ref.trim() || undefined, subdir: subdir.trim() || undefined, contract: contract.trim() || undefined },
    });

  const addFromCheckoutService = (loc: string, opts: { allow_subdir?: boolean } = {}): Promise<Service> =>
    services.addFromCheckout({ name: name.trim() || undefined, path: loc, ref: ref.trim() || undefined, ...opts });

  const finishAdd = (svc: Service) => {
    setWarnings(svc.warnings || []);
    onAdded(svc);
    setCheckoutIssue(null);
    if (!svc.warnings || svc.warnings.length === 0) {
      reset();
      setOpen(false);
    }
  };

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    const loc = location.trim();
    if (!loc) {
      setErr('Path or git URL is required.');
      return;
    }

    setSubmitting(true);
    setErr(null);
    setWarnings(null);
    setCheckoutIssue(null);
    try {
      const svc = isGit ? await addGitService(loc) : asTeamSource ? await addFromCheckoutService(loc) : await addLocalService(loc);
      finishAdd(svc);
    } catch (e) {
      if (!isGit && asTeamSource && e instanceof ApiClientError && e.details?.local_add === true) {
        setCheckoutIssue({ message: e.message, path: loc, offerSubdirRetry: looksLikeSubdirIssue(e.message) });
      } else {
        setErr(e instanceof ApiClientError ? e.message : String(e));
      }
    } finally {
      setSubmitting(false);
    }
  };

  // The two follow-ups offered in place of a plain toast when addFromCheckout
  // refuses with details.local_add: add the checkout as a local-only
  // service, or (subdirectory refusals only) retry committing it as the
  // team's git source with allow_subdir.
  const addAsLocalPath = async () => {
    if (!checkoutIssue) return;
    setSubmitting(true);
    try {
      finishAdd(await addLocalService(checkoutIssue.path));
    } catch (e) {
      setErr(e instanceof ApiClientError ? e.message : String(e));
      setCheckoutIssue(null);
    } finally {
      setSubmitting(false);
    }
  };

  const commitWithSubdir = async () => {
    if (!checkoutIssue) return;
    setSubmitting(true);
    try {
      finishAdd(await addFromCheckoutService(checkoutIssue.path, { allow_subdir: true }));
    } catch (e) {
      if (e instanceof ApiClientError && e.details?.local_add === true) {
        setCheckoutIssue({ message: e.message, path: checkoutIssue.path, offerSubdirRetry: looksLikeSubdirIssue(e.message) });
      } else {
        setErr(e instanceof ApiClientError ? e.message : String(e));
        setCheckoutIssue(null);
      }
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
        {!isGit && location.trim() && (
          <div className="flex flex-wrap items-center gap-2">
            <label className="flex items-center gap-2 text-xs text-slate-500">
              <input type="checkbox" checked={asTeamSource} onChange={(e) => setAsTeamSource(e.target.checked)} />
              Also commit as the team&apos;s git source (from the checkout&apos;s origin)
            </label>
            {asTeamSource && (
              <input
                value={ref}
                onChange={(e) => setRef(e.target.value)}
                placeholder="default branch"
                aria-label="ref"
                className="w-48 rounded border border-slate-300 bg-white px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-900"
              />
            )}
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
        {checkoutIssue && (
          <div className="rounded border border-amber-300 bg-amber-50 p-2 text-sm text-amber-800 dark:border-amber-900 dark:bg-amber-950 dark:text-amber-300">
            <p className="mb-2">{checkoutIssue.message}</p>
            <div className="flex flex-wrap gap-2">
              <button
                type="button"
                onClick={addAsLocalPath}
                disabled={submitting}
                className="rounded border border-amber-400 px-2 py-1 text-xs disabled:opacity-50 dark:border-amber-800"
              >
                Add as local path
              </button>
              {checkoutIssue.offerSubdirRetry && (
                <button
                  type="button"
                  onClick={commitWithSubdir}
                  disabled={submitting}
                  className="rounded border border-amber-400 px-2 py-1 text-xs disabled:opacity-50 dark:border-amber-800"
                >
                  Commit repository with subdir
                </button>
              )}
            </div>
          </div>
        )}
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
