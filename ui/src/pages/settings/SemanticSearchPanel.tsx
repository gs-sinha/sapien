// Settings > Semantic search (PLAN §34f item 5): off by default, turned on
// here with no daemon restart. GET seeds the form; Test tries a config
// without saving it; Save PUTs it and, on a refusal, offers "Save anyway"
// (force: true); the status line is seeded from the GET and kept live by
// `semantic.index` events while this panel is mounted, same pattern as
// state/repo.ts being fed by `workspace.repo`.
import { useEffect, useState } from 'react';
import { semanticSettings } from '../../api/client';
import { useAsync } from '../../lib/useAsync';
import { subscribe } from '../../state/events';
import { pushToast } from '../../state/toast';
import type {
  OllamaModelsResponse,
  SemanticIndexStatus,
  SemanticProviderKind,
  SemanticSettings,
  SemanticSettingsScope,
  SemanticTestResult,
  UpdateSemanticSettingsRequest,
} from '../../api/types';

const DEFAULT_BASE_URL: Record<SemanticProviderKind, string> = {
  ollama: 'http://localhost:11434',
  openai_compatible: 'https://api.openai.com/v1',
};

const OLLAMA_SUGGESTIONS: Array<{ model: string; label?: string }> = [
  { model: 'nomic-embed-text', label: 'default' },
  { model: 'mxbai-embed-large' },
  { model: 'all-minilm' },
  { model: 'bge-m3' },
];

const OPENAI_SUGGESTIONS = ['text-embedding-3-small', 'text-embedding-3-large'];

const buttonCls = 'rounded border border-slate-300 px-3 py-1.5 text-sm disabled:opacity-50 dark:border-slate-700';
const chipCls = 'rounded-full border border-slate-300 px-2 py-1 text-xs disabled:opacity-50 dark:border-slate-700';
const fieldCls = 'w-full max-w-md rounded border border-slate-300 bg-white px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-900';

function fmtNum(n?: number): string {
  return (n ?? 0).toLocaleString();
}

function statusLine(status: SemanticIndexStatus): string {
  switch (status.state) {
    case 'off':
      return 'Off.';
    case 'error':
      return status.error ? `Error: ${status.error}` : 'Error.';
    case 'indexing':
      return `Indexing ${fmtNum(status.embedded)} / ${fmtNum(status.total)}`;
    case 'ready':
      return `Ready · ${status.model ?? ''} · ${status.dim ?? '?'} dims · ${fmtNum(status.embedded)} embedded`;
    default:
      return status.state;
  }
}

export default function SemanticSearchPanel() {
  const { data, error, loading, reload } = useAsync(() => semanticSettings.get(), []);

  return (
    <section id="semantic" className="mb-6 rounded border border-slate-200 p-4 dark:border-slate-800">
      <h2 className="mb-2 text-sm font-semibold">Semantic search</h2>
      {loading && <p className="text-sm text-slate-400">Loading…</p>}
      {error && <p className="text-sm text-red-600">{error.message}</p>}
      {data && <SemanticForm key={data.source + data.kind} initial={data} onSaved={reload} />}
    </section>
  );
}

function SemanticForm({ initial, onSaved }: { initial: SemanticSettings; onSaved: () => void }) {
  const [enabled, setEnabled] = useState(initial.enabled);
  const [kind, setKind] = useState<SemanticProviderKind>(initial.kind || 'ollama');
  const [baseUrl, setBaseUrl] = useState(initial.base_url || DEFAULT_BASE_URL[initial.kind || 'ollama']);
  const [model, setModel] = useState(initial.model || '');
  const [scope, setScope] = useState<SemanticSettingsScope>(initial.source || 'user');
  const [apiKey, setApiKey] = useState('');
  const [apiKeyTouched, setApiKeyTouched] = useState(false);
  const [apiKeySet, setApiKeySet] = useState(initial.api_key_set);

  const [status, setStatus] = useState<SemanticIndexStatus>(initial.status);
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);
  const [offeringForce, setOfferingForce] = useState(false);
  const [testResult, setTestResult] = useState<SemanticTestResult | null>(null);
  const [testing, setTesting] = useState(false);
  const [reindexing, setReindexing] = useState(false);

  useEffect(
    () =>
      subscribe('semantic.index', (e) => {
        if (e.semanticIndex) setStatus((s) => ({ ...s, ...e.semanticIndex }));
      }),
    [],
  );

  const switchKind = (next: SemanticProviderKind) => {
    setKind(next);
    // Only replace the base URL if it still holds the previous provider's
    // own default (or is empty) -- never clobber something the user typed.
    setBaseUrl((prev) => (!prev || prev === DEFAULT_BASE_URL[kind] ? DEFAULT_BASE_URL[next] : prev));
  };

  const buildRequest = (force?: boolean): UpdateSemanticSettingsRequest => ({
    enabled,
    kind,
    base_url: baseUrl || undefined,
    model: model || undefined,
    scope,
    ...(apiKeyTouched ? { api_key: apiKey } : {}),
    ...(force ? { force: true } : {}),
  });

  const save = async (force?: boolean) => {
    setSaving(true);
    setSaveError(null);
    try {
      const next = await semanticSettings.update(buildRequest(force));
      setStatus(next.status);
      setApiKeySet(next.api_key_set);
      setApiKeyTouched(false);
      setApiKey('');
      setOfferingForce(false);
      pushToast('success', 'Semantic search settings saved.');
      onSaved();
    } catch (e) {
      const message = e instanceof Error ? e.message : 'Save failed.';
      setSaveError(message);
      setOfferingForce(true);
    } finally {
      setSaving(false);
    }
  };

  const test = async () => {
    setTesting(true);
    setTestResult(null);
    try {
      setTestResult(await semanticSettings.test(buildRequest()));
    } catch (e) {
      setTestResult({ ok: false, error: e instanceof Error ? e.message : 'Test failed.' });
    } finally {
      setTesting(false);
    }
  };

  const reindex = async () => {
    setReindexing(true);
    try {
      await semanticSettings.reindex();
      pushToast('info', 'Reindexing started.');
    } catch (e) {
      pushToast('error', e instanceof Error ? e.message : 'Reindex failed.');
    } finally {
      setReindexing(false);
    }
  };

  return (
    <div className="space-y-4">
      <label className="flex items-center gap-2 text-sm">
        <input type="checkbox" checked={enabled} onChange={(e) => setEnabled(e.target.checked)} />
        Enable semantic search
      </label>

      {!enabled && (
        <p className="text-sm text-slate-500">
          Semantic search adds meaning-based ranking on top of keyword search across operations, docs, memories and
          examples, so a vaguely-worded query still finds the right one — it is optional, off by default, and can be
          turned off again at any time.
        </p>
      )}

      {enabled && (
        <>
          <div className="flex flex-wrap items-center gap-4 text-sm">
            <label className="flex items-center gap-1.5">
              <input type="radio" name="semantic-kind" checked={kind === 'ollama'} onChange={() => switchKind('ollama')} />
              Ollama
            </label>
            <label className="flex items-center gap-1.5">
              <input
                type="radio"
                name="semantic-kind"
                checked={kind === 'openai_compatible'}
                onChange={() => switchKind('openai_compatible')}
              />
              OpenAI-compatible
            </label>
          </div>

          <label className="block">
            <span className="mb-1 block text-xs text-slate-500">Base URL</span>
            <input value={baseUrl} onChange={(e) => setBaseUrl(e.target.value)} className={fieldCls} />
          </label>

          {kind === 'ollama' ? (
            <OllamaModelPicker baseUrl={baseUrl} model={model} onSelect={setModel} />
          ) : (
            <>
              <label className="block">
                <span className="mb-1 block text-xs text-slate-500">Model</span>
                <input
                  value={model}
                  onChange={(e) => setModel(e.target.value)}
                  placeholder="text-embedding-3-small"
                  className={fieldCls}
                />
              </label>
              <div className="flex flex-wrap gap-2">
                {OPENAI_SUGGESTIONS.map((m) => (
                  <button key={m} type="button" onClick={() => setModel(m)} className={chipCls}>
                    {m}
                  </button>
                ))}
              </div>
              <label className="block">
                <span className="mb-1 block text-xs text-slate-500">API key</span>
                <input
                  type="password"
                  value={apiKey}
                  onChange={(e) => {
                    setApiKey(e.target.value);
                    setApiKeyTouched(true);
                  }}
                  placeholder={apiKeySet ? 'unchanged' : ''}
                  className={fieldCls}
                />
              </label>
            </>
          )}

          <div className="flex flex-wrap items-center gap-4 text-sm">
            <label className="flex items-center gap-1.5">
              <input type="radio" name="semantic-scope" checked={scope === 'user'} onChange={() => setScope('user')} />
              All workspaces
            </label>
            <label className="flex items-center gap-1.5">
              <input type="radio" name="semantic-scope" checked={scope === 'workspace'} onChange={() => setScope('workspace')} />
              This workspace
            </label>
          </div>
        </>
      )}

      <div className="flex flex-wrap items-center gap-2">
        {enabled && (
          <button type="button" disabled={testing} onClick={test} className={buttonCls}>
            {testing ? 'Testing…' : 'Test connection'}
          </button>
        )}
        {testResult && (
          <span className={testResult.ok ? 'text-xs text-emerald-600' : 'text-xs text-red-600'}>
            {testResult.ok
              ? `OK${testResult.dim ? ` · ${testResult.dim} dims` : ''}${testResult.latency_ms !== undefined ? ` · ${testResult.latency_ms}ms` : ''}`
              : testResult.error || 'Failed'}
          </span>
        )}
        <button type="button" disabled={saving} onClick={() => save()} className={buttonCls}>
          {saving ? 'Saving…' : 'Save'}
        </button>
        {enabled && (
          <button type="button" disabled={reindexing} onClick={reindex} className={buttonCls}>
            {reindexing ? 'Reindexing…' : 'Reindex'}
          </button>
        )}
      </div>

      {saveError && (
        <div className="rounded border border-amber-300 bg-amber-50 p-2 text-xs text-amber-800 dark:border-amber-900 dark:bg-amber-950 dark:text-amber-300">
          <p>{saveError}</p>
          {offeringForce && (
            <button
              type="button"
              disabled={saving}
              onClick={() => save(true)}
              className="mt-1 rounded border border-amber-400 px-2 py-0.5 disabled:opacity-50"
            >
              Save anyway
            </button>
          )}
        </div>
      )}

      <p className="text-sm text-slate-600 dark:text-slate-400">{statusLine(status)}</p>
    </div>
  );
}

// The Ollama-specific half of the model field: check reachability, offer
// the installed models as the picker, and suggestion chips (with a Pull
// button and a live progress bar) for the common embedding models that
// aren't installed yet.
function OllamaModelPicker({ baseUrl, model, onSelect }: { baseUrl: string; model: string; onSelect: (m: string) => void }) {
  const [tick, setTick] = useState(0);
  const { data, error, loading } = useAsync<OllamaModelsResponse>(() => semanticSettings.ollama(baseUrl), [baseUrl, tick]);
  const [pulls, setPulls] = useState<Record<string, { completed: number; total: number; done: boolean; error?: string }>>({});

  useEffect(
    () =>
      subscribe('semantic.pull', (e) => {
        const p = e.semanticPull;
        if (!p) return;
        setPulls((prev) => ({ ...prev, [p.model]: { completed: p.completed, total: p.total, done: p.done, error: p.error } }));
        // Refresh the installed-models list once a pull finishes, whether it
        // succeeded or failed, so a completed model moves out of "missing".
        if (p.done) setTick((t) => t + 1);
      }),
    [],
  );

  const pull = async (m: string) => {
    try {
      await semanticSettings.ollamaPull({ model: m, base_url: baseUrl });
    } catch (e) {
      pushToast('error', e instanceof Error ? e.message : `Pull ${m} failed.`);
    }
  };

  if (loading) return <p className="text-sm text-slate-400">Checking Ollama…</p>;
  if (error) return <p className="text-sm text-red-600">{error.message}</p>;
  if (!data) return null;

  if (!data.reachable) {
    return (
      <div className="rounded border border-amber-300 bg-amber-50 p-2 text-sm text-amber-800 dark:border-amber-900 dark:bg-amber-950 dark:text-amber-300">
        <p>Ollama is not running at {data.base_url}.</p>
        <p className="mt-1">
          <a href="https://ollama.com/download" target="_blank" rel="noreferrer" className="underline">
            Install Ollama
          </a>
        </p>
        <button type="button" onClick={() => setTick((t) => t + 1)} className="mt-2 rounded border border-amber-400 px-2 py-0.5 text-xs">
          Retry
        </button>
      </div>
    );
  }

  const installed = new Set(data.models.map((m) => m.name));
  const missing = OLLAMA_SUGGESTIONS.filter((s) => !installed.has(s.model));

  return (
    <div className="space-y-2">
      <label className="block">
        <span className="mb-1 block text-xs text-slate-500">Model</span>
        <select value={installed.has(model) ? model : ''} onChange={(e) => onSelect(e.target.value)} className={fieldCls}>
          <option value="">select an installed model…</option>
          {data.models.map((m) => (
            <option key={m.name} value={m.name}>
              {m.name}
            </option>
          ))}
        </select>
      </label>
      {missing.length > 0 && (
        <div className="flex flex-wrap items-start gap-3">
          {missing.map((s) => {
            const p = pulls[s.model];
            const inProgress = !!p && !p.done;
            return (
              <div key={s.model} className="flex flex-col gap-1">
                <button type="button" onClick={() => pull(s.model)} disabled={inProgress} className={chipCls}>
                  {inProgress ? 'Pulling…' : 'Pull'} {s.model}
                  {s.label ? ` (${s.label})` : ''}
                </button>
                {inProgress && (
                  <div className="h-1 w-32 overflow-hidden rounded bg-slate-200 dark:bg-slate-800">
                    <div
                      className="h-1 bg-sky-600"
                      style={{ width: `${p.total ? Math.min(100, (p.completed / p.total) * 100) : 0}%` }}
                    />
                  </div>
                )}
                {p?.error && <span className="text-xs text-red-600">{p.error}</span>}
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}
