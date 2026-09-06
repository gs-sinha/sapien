// Extra client functions for wave-2 pages, kept out of api/client.ts per the
// file ownership split (that file is hand-maintained by whoever owns the
// mirror of internal/server's route table as a whole). Mirrors client.ts's
// own conventions: credentials included, errors normalized to
// ApiClientError.
import { ApiClientError } from './client';
import type { Hint } from './types-try';

async function getJSON<T>(path: string): Promise<T> {
  let res: Response;
  try {
    res = await fetch(path, { credentials: 'include', headers: { Accept: 'application/json' } });
  } catch (err) {
    throw new ApiClientError('E_NETWORK', err instanceof Error ? err.message : 'network request failed');
  }

  const text = await res.text();
  let body: unknown;
  if (text) {
    try {
      body = JSON.parse(text);
    } catch {
      body = undefined;
    }
  }

  if (!res.ok) {
    if (body && typeof body === 'object' && 'code' in body) {
      const b = body as { code: string; message?: string };
      throw new ApiClientError(b.code, b.message || res.statusText);
    }
    throw new ApiClientError('E_HTTP_' + res.status, (text || res.statusText).slice(0, 500));
  }

  return body as T;
}

function isNotFound(err: unknown): boolean {
  return err instanceof ApiClientError && (err.code === 'E_HTTP_404' || /NOT_FOUND/i.test(err.code));
}

// getRunHints fetches "might explain it" diagnostic hints for a run's
// failed steps: GET /v1/runs/{id}/hints (internal/diagnose, wired to the
// server as handleRunHints). The route may not exist on an older daemon,
// or a run may simply have nothing to explain -- both surface as 404 and
// are treated as "no hints" rather than an error, so a Try It result panel
// can call this unconditionally after a failed call.
export async function getRunHints(runId: string): Promise<Hint[]> {
  try {
    const res = await getJSON<{ hints?: Hint[] } | Hint[]>(`/v1/runs/${encodeURIComponent(runId)}/hints`);
    return Array.isArray(res) ? res : res.hints ?? [];
  } catch (err) {
    if (isNotFound(err)) return [];
    throw err;
  }
}
