// The "Source" section of a service page: which source this machine reads
// the service from ("listening to") and which it could read from instead
// ("can listen to"), with the one-click switch between them.
//
// A git source in the committed workspace is read from a managed clone that
// every sync resets, so nothing can be written into it. Binding a local
// checkout (PUT /v1/services/{name}/binding, recorded in the gitignored
// sapien.workspace.local.yaml) makes the service writable on this machine,
// so service-scoped memories, examples and flows ride the developer's own
// branch; DELETE puts the team source back. Mirrors `sapien service
// bind|unbind`.
import { useEffect, useState } from 'react';
import type { ReactNode } from 'react';
import { services } from '../../api/client';
import { KeyValue } from '../../components/KeyValue';
import { pushToast } from '../../state/toast';
import type { BindingInfo, LocalCheckout, Service, ServiceBinding, Source } from '../../api/types';

export function shortCommit(commit?: string): string {
  return commit ? commit.slice(0, 7) : '';
}

// "team · <url> @ <ref>": the committed git source every other machine reads.
export function teamLabel(src?: Source): string {
  if (!src) return 'team';
  const where = src.url || src.path || 'git';
  return `team · ${where}${src.ref ? ` @ ${src.ref}` : ''}`;
}

// "local · <path> · branch <b> · <commit7> · N uncommitted here": what this
// machine reads when bound to a checkout. Dirty only shows when > 0.
export function checkoutLabel(c: LocalCheckout): string {
  const parts = [`local · ${c.path}`];
  if (c.branch) parts.push(`branch ${c.branch}`);
  if (c.commit) parts.push(shortCommit(c.commit));
  if (c.dirty && c.dirty > 0) parts.push(`${c.dirty} uncommitted here`);
  return parts.join(' · ');
}

// What an unbound daemon (no `binding` on the Service, no /binding route)
// would have said: derived from the committed source alone, no actions.
function fallbackBinding(service: Service): ServiceBinding {
  if (service.source.type === 'git') return { mode: 'team', team: service.source, writable: false };
  return { mode: 'local', local: { path: service.source.path || '' }, writable: true };
}

const buttonCls = 'rounded border border-slate-300 px-2 py-1 text-xs disabled:opacity-50 dark:border-slate-700';

export function BindingPanel({ service, onChanged }: { service: Service; onChanged: () => void }) {
  const name = service.name;
  const [info, setInfo] = useState<BindingInfo | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [path, setPath] = useState('');
  const [busy, setBusy] = useState(false);
  // Bumped after a bind/unbind so the candidates and the binding refresh
  // even if the page around this panel did not remount.
  const [tick, setTick] = useState(0);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const next = await services.binding(name);
        if (cancelled) return;
        setInfo(next);
        setLoadError(null);
      } catch (e) {
        if (cancelled) return;
        setLoadError(e instanceof Error ? e.message : String(e));
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [name, tick]);

  const hasBinding = !!(info?.binding || service.binding);
  const binding: ServiceBinding = info?.binding || service.binding || fallbackBinding(service);
  const candidates = info?.candidates ?? [];
  const team = binding.team || (service.source.type === 'git' ? service.source : undefined);

  const bind = async (target: string) => {
    const p = target.trim();
    if (!p) return;
    setBusy(true);
    try {
      await services.bind(name, p);
      pushToast('success', `${name} now reads from ${p}.`);
      setPath('');
      setTick((t) => t + 1);
      onChanged();
    } catch (e) {
      pushToast('error', e instanceof Error ? e.message : 'Bind failed.');
    } finally {
      setBusy(false);
    }
  };

  const unbind = async () => {
    setBusy(true);
    try {
      await services.unbind(name);
      pushToast('success', `${name} now reads from the team source.`);
      setTick((t) => t + 1);
      onChanged();
    } catch (e) {
      pushToast('error', e instanceof Error ? e.message : 'Unbind failed.');
    } finally {
      setBusy(false);
    }
  };

  let listening: string;
  let canListen: ReactNode;
  if (binding.mode === 'team') {
    const commit = shortCommit(service.commit);
    listening = `${teamLabel(team)}${commit ? ` (${commit})` : ''}`;
    canListen = hasBinding ? (
      <div className="space-y-2">
        <div>a local checkout</div>
        {candidates.length > 0 && (
          <div className="flex flex-wrap gap-2">
            {candidates.map((c) => (
              <button
                key={c.path}
                type="button"
                disabled={busy}
                onClick={() => bind(c.path)}
                title="Read this service from this checkout"
                className={`${buttonCls} font-mono`}
              >
                {checkoutLabel(c)}
              </button>
            ))}
          </div>
        )}
        <form
          onSubmit={(e) => {
            e.preventDefault();
            bind(path);
          }}
          className="flex flex-wrap items-center gap-2"
        >
          <input
            value={path}
            onChange={(e) => setPath(e.target.value)}
            placeholder={`~/code/${name}`}
            aria-label="Local checkout path"
            className="w-72 rounded border border-slate-300 bg-white px-2 py-1 font-mono text-xs dark:border-slate-700 dark:bg-slate-900"
          />
          <button type="submit" disabled={busy || !path.trim()} className={buttonCls}>
            {busy ? 'Switching…' : 'Read from checkout'}
          </button>
        </form>
      </div>
    ) : (
      <span className="text-slate-400">a local checkout (`sapien service bind {name} &lt;path&gt;`)</span>
    );
  } else {
    listening = binding.local ? checkoutLabel(binding.local) : `local · ${service.source.path || ''}`;
    canListen = team ? (
      <div className="flex flex-wrap items-center gap-2">
        <span className="font-mono text-xs">{teamLabel(team)}</span>
        {hasBinding && (
          <button type="button" disabled={busy} onClick={unbind} className={buttonCls}>
            {busy ? 'Switching…' : 'Read from team source'}
          </button>
        )}
      </div>
    ) : (
      <span className="text-slate-400">nothing else: a local source in the committed workspace has no team source to fall back to.</span>
    );
  }

  return (
    <section className="mb-4 rounded border border-slate-200 p-3 dark:border-slate-800">
      <h2 className="mb-2 text-sm font-semibold">Source</h2>
      <KeyValue
        pairs={[
          ['listening to', <span className="font-mono text-xs">{listening}</span>],
          ['can listen to', canListen],
        ]}
      />
      {!binding.writable && (
        <p className="mt-2 text-xs text-slate-500">
          Service-scoped memories, examples and flows are read-only while {name} is read from the team source; read from a
          checkout to write them.
        </p>
      )}
      {loadError && <p className="mt-2 text-xs text-amber-700 dark:text-amber-400">Could not load checkout candidates: {loadError}</p>}
    </section>
  );
}
