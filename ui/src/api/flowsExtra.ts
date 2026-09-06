// New client-side helpers for the flows/runs pages that don't belong in the
// shared api/client.ts (per the wave-2 ownership split): fetching diagnose
// hints for a run (a route that may not exist yet -- see getRunHints), and
// recovering a step's path/query params from its recorded request URL so
// "rerun step alone" can call the operation again with the same params.
import { ApiClientError, callOperation, operations } from './client';
import type { CallRequest, Operation, Run, StepResult } from './types';
import type { Hint, HintsResponse } from './types-runs';

// getRunHints wraps GET /v1/runs/{id}/hints. The route is added by another
// concurrent wave-2 agent (internal/diagnose is not wired to any /v1 route
// as of this writing), so any failure -- 404 while it doesn't exist yet, a
// network error, or a future shape change -- is treated as "no hints"
// rather than surfaced as a page error; the hints panel is a supplementary
// "might explain it" aid, not core to reading a run.
export async function getRunHints(runId: string): Promise<Hint[]> {
  try {
    const res = await fetch(`/v1/runs/${encodeURIComponent(runId)}/hints`, {
      credentials: 'include',
      headers: { Accept: 'application/json' },
    });
    if (!res.ok) return [];
    const body = (await res.json()) as HintsResponse;
    return body.hints || [];
  } catch {
    return [];
  }
}

// paramsFromRequestURL recovers path/query parameter values from a step's
// recorded request URL by matching the operation's HTTP path template
// against the tail of the URL's pathname (path params) and reading the
// operation's declared query params straight off the URL's search string.
// Returns undefined when the operation has no HTTP binding or nothing
// matches, so the caller can fall back to sending just body+headers.
export function paramsFromRequestURL(op: Operation, rawUrl: string): Record<string, unknown> | undefined {
  if (!op.http) return undefined;
  let u: URL;
  try {
    u = new URL(rawUrl);
  } catch {
    return undefined;
  }

  const out: Record<string, unknown> = {};
  const names: string[] = [];
  const escaped = op.http.path
    .replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
    .replace(/\\\{([^}]+)\\\}/g, (_m, name: string) => {
      names.push(name);
      return '([^/]+)';
    });
  const match = u.pathname.match(new RegExp(escaped + '$'));
  if (match) {
    names.forEach((name, i) => {
      out[name] = decodeURIComponent(match[i + 1]);
    });
  }

  for (const p of op.params || []) {
    if (p.in === 'query' && u.searchParams.has(p.name)) {
      out[p.name] = u.searchParams.get(p.name) ?? '';
    }
  }

  return Object.keys(out).length > 0 ? out : undefined;
}

// rerunStepAlone replays one recorded step as a fresh single-step run
// (POST /v1/call): same operation, body, and headers, plus whatever
// path/query params can be recovered from the recorded request's URL. When
// the operation can't be resolved (deleted, renamed) or nothing matches,
// it still sends body+headers and lets the server validate.
export async function rerunStepAlone(step: StepResult, env: string): Promise<Run> {
  if (!step.operation) {
    throw new ApiClientError('E_NO_OPERATION', 'this step has no recorded operation to rerun');
  }

  let params: Record<string, unknown> | undefined;
  if (step.request?.url) {
    try {
      const op = await operations.get(step.operation);
      params = paramsFromRequestURL(op, step.request.url);
    } catch {
      // Operation lookup failed (e.g. it no longer exists); fall back to
      // body+headers only, same as when there's simply nothing to recover.
    }
  }

  const req: CallRequest = {
    operation: step.operation,
    params,
    body: step.request?.body,
    headers: step.request?.headers,
    env,
    trigger: 'ui',
  };
  return callOperation(req);
}
