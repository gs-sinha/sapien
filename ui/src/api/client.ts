// Thin fetch wrapper for the Sapien daemon's HTTP API (internal/server).
//
// Every request sends credentials so the `sapien_session` cookie (set by
// `GET /ui/session?token=`) authenticates it; in dev, vite.config.ts's proxy
// injects a Bearer header instead so the cookie is never needed locally.
//
// Errors are normalized to ApiClientError, whose shape mirrors the daemon's
// error body (internal/errs.Error / httputil.go writeError): code, message,
// details, source, hint. A request that never reaches the server (network
// failure, daemon not running) is mapped to code "E_NETWORK" so callers can
// treat every failure uniformly.
import { setSessionStale } from '../state/daemon';
import { currentWorkspace } from '../state/workspace';
import type { WorkspaceInfo } from '../state/workspace';
import type {
  AddServiceRequest,
  BindingInfo,
  CallRequest,
  CreateFlowRequest,
  ContextBundle,
  ContextRequest,
  DefaultEnvironmentResponse,
  Doc,
  DocSearchResult,
  Environment,
  Event,
  ExampleFromRun,
  ExampleQueryParams,
  Field,
  Flow,
  FlowOwnerKind,
  FlowSummary,
  FlowYAMLRequest,
  FrictionPreview,
  FrictionReport,
  HealthResponse,
  Memory,
  MemoryQueryParams,
  NamedSchema,
  Operation,
  PromotionTarget,
  PurgeRunsResponse,
  RequestExample,
  Run,
  RunFilter,
  RunFlowSourceRequest,
  RunOptionsWire,
  SavedExample,
  ScoredMemory,
  SearchResult,
  Service,
  StepResult,
  Subject,
  ValidationResult,
  Workspace,
} from './types';

// normalizeNulls walks a parsed JSON value and replaces `null` with `[]` for
// any object key in NULLABLE_ARRAY_KEYS. Go marshals a nil slice as JSON
// `null` whenever the field lacks `omitempty` (e.g. Flow.steps,
// ContextBundle's tiers, FlowContext.steps) -- fields the hand-mirrored
// types.ts marks as *required* arrays, so `flow.steps.map(...)` or
// `for (const d of bundle.docs)` crashes at runtime even though TypeScript
// swears the value can't be null. Every parsed response body is normalized
// here so the rest of the app can treat every declared array field as an
// actual array, matching what its type already promises.
//
// The key set below is built from every array-typed field in api/types.ts,
// with one exclusion rule: a key name that is ALSO used for a non-array
// (object/scalar) field anywhere in the wire shapes is left out entirely,
// because rewriting that key by name alone would corrupt the non-array
// shape. Concretely excluded, and why:
//   - `params`   -- Operation.params is Param[], but Step.params is an
//                   ExplicitParams object (a flow's steps carry both shapes
//                   in the very same GET /v1/flows/:id response).
//   - `required` -- Schema.required is string[], but Param.required and
//                   Body.required are plain booleans.
//   - `headers`  -- Redaction.headers is string[], but everywhere else
//                   (RequestRecord, ResponseRecord, Step, SavedExample,
//                   CallRequest, ExplicitParams, Response.headers) it's a
//                   Record<string, string | Schema> map.
//   - `response` -- OperationContext.response is string[], but
//                   StepResult.response is a ResponseRecord object.
//   - `services` -- Workspace.services is ServiceRef[], but
//                   Environment.services is a Record<string, ServiceEnv> map.
//   - `body`     -- never array-typed (always `unknown`); left out on
//                   principle, same as other map/scalar-shaped fields like
//                   `inputs`, `environments`, `vars`, `extract`, `out`.
export const NULLABLE_ARRAY_KEYS = new Set([
  // service.go
  'owners', 'concepts', 'contract_files', 'warnings', 'accepted_warnings',
  // operation.go / schema
  'property_order', 'variants', 'enum', 'tags', 'responses', 'security', 'used_by',
  // doc.go
  'refs', 'sections', 'docs',
  // flow.go
  'assert', 'steps', 'operations', 'suggestions', 'diagnostics',
  // run.go
  'assertions',
  // memory.go
  'reasons',
  // environment.go
  'json_paths',
  // events.go
  'added', 'removed', 'changed',
  // search.go / context bundle
  'matched_on', 'examples', 'flows', 'runs', 'memories', 'scopes',
  // engine.go (BindingInfo)
  'candidates',
  // diagnose (bypasses this client today, kept for when it doesn't)
  'hints',
]);

export function normalizeNulls(value: unknown): unknown {
  if (Array.isArray(value)) {
    return value.map(normalizeNulls);
  }
  if (value && typeof value === 'object') {
    const out: Record<string, unknown> = {};
    for (const [k, v] of Object.entries(value as Record<string, unknown>)) {
      out[k] = v === null && NULLABLE_ARRAY_KEYS.has(k) ? [] : normalizeNulls(v);
    }
    return out;
  }
  return value;
}

export class ApiClientError extends Error {
  code: string;
  details?: Record<string, unknown>;
  source?: { file: string; pointer?: string; line?: number; column?: number };
  hint?: string;

  constructor(code: string, message: string, opts?: { details?: Record<string, unknown>; source?: ApiClientError['source']; hint?: string }) {
    super(message);
    this.name = 'ApiClientError';
    this.code = code;
    this.details = opts?.details;
    this.source = opts?.source;
    this.hint = opts?.hint;
  }
}

function buildQuery(params: object = {}): string {
  const usp = new URLSearchParams();
  for (const [key, value] of Object.entries(params as Record<string, unknown>)) {
    if (value === undefined || value === null || value === '') continue;
    if (Array.isArray(value)) {
      for (const v of value) usp.append(key, String(v));
    } else {
      usp.set(key, String(value));
    }
  }
  const qs = usp.toString();
  return qs ? `?${qs}` : '';
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  let res: Response;
  // One daemon serves many workspaces; this header picks which one. Absent
  // (the default until the user switches) means the daemon's primary
  // workspace, which is what every route did before switching existed.
  const ws = currentWorkspace();
  try {
    res = await fetch(path, {
      credentials: 'include',
      headers: {
        ...(init?.body ? { 'Content-Type': 'application/json' } : {}),
        Accept: 'application/json',
        ...(ws ? { 'X-Sapien-Workspace': ws } : {}),
        ...(init?.headers || {}),
      },
      ...init,
    });
  } catch (err) {
    throw new ApiClientError('E_NETWORK', err instanceof Error ? err.message : 'network request failed');
  }

  // 401 here means the session cookie is for a daemon that no longer
  // exists: `sapien serve` mints a new bearer token on every start, so a
  // tab that outlived a restart keeps sending the old one. Without this
  // the app looked connected -- the daemon is reachable, /v1/health is
  // unauthenticated and answers happily -- while every actual request
  // failed. A success clears it again, which is what a relaunch's own
  // /ui/session tab does for every tab at this origin.
  if (res.status === 401) {
    setSessionStale(true);
  } else if (res.ok) {
    setSessionStale(false);
  }

  if (res.status === 204) {
    return undefined as T;
  }

  const text = await res.text();
  let body: unknown = undefined;
  if (text) {
    try {
      body = normalizeNulls(JSON.parse(text));
    } catch {
      body = undefined;
    }
  }

  if (!res.ok) {
    if (body && typeof body === 'object' && 'code' in body) {
      const b = body as { code: string; message?: string; details?: Record<string, unknown>; source?: ApiClientError['source']; hint?: string };
      throw new ApiClientError(b.code, b.message || res.statusText, { details: b.details, source: b.source, hint: b.hint });
    }
    throw new ApiClientError('E_HTTP_' + res.status, (text || res.statusText).slice(0, 500));
  }

  return body as T;
}

function get<T>(path: string): Promise<T> {
  return request<T>(path, { method: 'GET' });
}
function post<T>(path: string, body?: unknown): Promise<T> {
  return request<T>(path, { method: 'POST', body: body !== undefined ? JSON.stringify(body) : undefined });
}
function put<T>(path: string, body?: unknown): Promise<T> {
  return request<T>(path, { method: 'PUT', body: body !== undefined ? JSON.stringify(body) : undefined });
}
function patch<T>(path: string, body?: unknown): Promise<T> {
  return request<T>(path, { method: 'PATCH', body: body !== undefined ? JSON.stringify(body) : undefined });
}
function del<T>(path: string): Promise<T> {
  return request<T>(path, { method: 'DELETE' });
}

// ---- health / workspace ----

export function getHealth(): Promise<HealthResponse> {
  return get('/v1/health');
}

export function getWorkspace(): Promise<Workspace> {
  return get('/v1/workspace');
}

// ---- services ----

// A GET/POST whose entire response body is a list has no object key for
// normalizeNulls to key off of -- Go still marshals a nil slice as a bare
// top-level `null` (not `[]`) for these, so every list-shaped endpoint below
// falls back to `[]` itself rather than relying on the recursive normalizer.
function orEmpty<T>(p: Promise<T[]>): Promise<T[]> {
  return p.then((v) => v ?? []);
}

export const services = {
  list: (): Promise<Service[]> => orEmpty(get('/v1/services')),
  get: (id: string): Promise<Service> => get(`/v1/services/${encodeURIComponent(id)}`),
  add: (req: AddServiceRequest): Promise<Service> => post('/v1/services', req),
  remove: (id: string): Promise<void> => del(`/v1/services/${encodeURIComponent(id)}`),
  sync: (id?: string): Promise<Service[]> =>
    orEmpty(id ? post(`/v1/services/${encodeURIComponent(id)}/sync`) : post('/v1/services/sync')),
  reindex: (): Promise<void> => post('/v1/services/reindex'),
  // Which source this machine reads the service from, plus local checkouts
  // of the same repository it could read from instead. bind/unbind switch
  // between a local checkout and the committed team source (recorded in the
  // gitignored sapien.workspace.local.yaml); both answer the updated Service.
  binding: (id: string): Promise<BindingInfo> => get(`/v1/services/${encodeURIComponent(id)}/binding`),
  bind: (id: string, path: string): Promise<Service> => put(`/v1/services/${encodeURIComponent(id)}/binding`, { path }),
  unbind: (id: string): Promise<Service> => del(`/v1/services/${encodeURIComponent(id)}/binding`),
};

// ---- operations / schemas / docs ----

export interface OperationSearchParams {
  query?: string;
  service?: string;
  method?: string;
  limit?: number;
  include_deprecated?: boolean;
}

export const operations = {
  // With `query` set this hits the search endpoint (scored results); without
  // it, the plain catalog list for the service (or every service).
  search: (params: OperationSearchParams = {}): Promise<SearchResult[] | Operation[]> => {
    const { query, ...rest } = params;
    return orEmpty(get(`/v1/operations${buildQuery({ q: query, ...rest })}`)) as Promise<SearchResult[] | Operation[]>;
  },
  resolve: (ref: string): Promise<Operation> => get(`/v1/operations/resolve${buildQuery({ ref })}`),
  get: (id: string): Promise<Operation> => get(`/v1/operations/${encodeURIComponent(id)}`),
  fields: (id: string): Promise<Field[]> => orEmpty(get(`/v1/operations/${encodeURIComponent(id)}/fields`)),
  // A ready-to-send request: the best of a verified example, a saved one, the
  // contract's own example, and one synthesized from the schema. `fields:
  // 'all'` synthesizes every field rather than the required ones. Resolved by
  // the daemon (internal/example.Resolve) so the UI, get_api, and the CLI all
  // answer "what does a call look like?" identically.
  example: (id: string, opts: { fields?: 'all' } = {}): Promise<RequestExample> =>
    get(`/v1/operations/${encodeURIComponent(id)}/example${buildQuery({ fields: opts.fields })}`),
};

export const schemas = {
  get: (service: string, name: string): Promise<NamedSchema> =>
    get(`/v1/schemas/${encodeURIComponent(service)}/${encodeURIComponent(name)}`),
};

export interface DocListParams {
  query?: string;
  service?: string;
  limit?: number;
}

// encodeDocPath percent-encodes each segment of a doc path while keeping the
// "/" separators, so a contract-embedded doc such as "contract#tag:Awb" is
// not cut at the "#" (a URL fragment) before it reaches the daemon.
function encodeDocPath(path: string): string {
  return path.split('/').map(encodeURIComponent).join('/');
}

export const docs = {
  // Not routed through orEmpty: that helper's own `T[]` inference collapses
  // a `DocSearchResult[] | Doc[]` union return into an unsound
  // `(DocSearchResult | Doc)[]`, so the null-to-[] fallback is inlined here
  // instead, with `get`'s type parameter given explicitly.
  list: async (params: DocListParams = {}): Promise<DocSearchResult[] | Doc[]> => {
    const { query, ...rest } = params;
    const res = await get<DocSearchResult[] | Doc[]>(`/v1/docs${buildQuery({ q: query, ...rest })}`);
    return res ?? [];
  },
  // `path` may itself contain slashes (e.g. "docs/allocation.md") or a
  // "contract#tag:name" fragment; section is resolved client-side against
  // the full doc's sections (there is no server-side section query param),
  // mirroring internal/cli/docs.go's findSection.
  get: async (service: string, path: string, section?: string): Promise<Doc> => {
    const doc = await get<Doc>(`/v1/docs/${encodeURIComponent(service)}/${encodeDocPath(path)}`);
    if (!section) return doc;
    const sec = findDocSection(doc, section);
    return sec ? { ...doc, sections: [sec] } : doc;
  },
};

function slugify(s: string): string {
  return s
    .toLowerCase()
    .trim()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/(^-|-$)/g, '');
}

export function findDocSection(doc: Doc, want: string) {
  const wantSlug = slugify(want);
  for (const sec of doc.sections || []) {
    if (sec.heading.localeCompare(want, undefined, { sensitivity: 'base' }) === 0) return sec;
    if (slugify(sec.heading) === wantSlug) return sec;
    if (sec.id.endsWith('#' + want) || sec.id.endsWith('#' + wantSlug)) return sec;
  }
  return undefined;
}

// ---- call ----

export function callOperation(req: CallRequest): Promise<Run> {
  return post('/v1/call', req);
}

// ---- flows ----

export const flows = {
  list: (q?: string): Promise<FlowSummary[]> => orEmpty(get(`/v1/flows${buildQuery({ q })}`)),
  get: (id: string): Promise<Flow> => get(`/v1/flows/${encodeURIComponent(id)}`),
  create: (req: CreateFlowRequest): Promise<Flow> => post('/v1/flows', req),
  update: (id: string, req: FlowYAMLRequest): Promise<Flow> => put(`/v1/flows/${encodeURIComponent(id)}`, req),
  delete: (id: string): Promise<void> => del(`/v1/flows/${encodeURIComponent(id)}`),
  validate: (req: FlowYAMLRequest): Promise<ValidationResult> => post('/v1/flows/validate', req),
  parse: (req: FlowYAMLRequest): Promise<Flow> => post('/v1/flows/parse', req),
  reference: (topic?: string): Promise<string> => get(`/v1/flows/reference${buildQuery({ topic })}`),
  run: (id: string, opts: RunOptionsWire = {}): Promise<Run> => post(`/v1/flows/${encodeURIComponent(id)}/run`, opts),
  // Move a flow between tiers (local -> workspace -> service); ownerId names
  // the service for the service tier. Mirrors `sapien flow promote`.
  rescope: (id: string, ownerKind: FlowOwnerKind, ownerId?: string): Promise<Flow> =>
    post(`/v1/flows/${encodeURIComponent(id)}/rescope`, { owner_kind: ownerKind, ...(ownerId ? { owner_id: ownerId } : {}) }),
};

// ---- runs ----

export const runs = {
  list: (filter: RunFilter = {}): Promise<Run[]> => orEmpty(get(`/v1/runs${buildQuery(filter)}`)),
  get: (id: string): Promise<Run> => get(`/v1/runs/${encodeURIComponent(id)}`),
  step: (id: string, step: string): Promise<StepResult> =>
    get(`/v1/runs/${encodeURIComponent(id)}/steps/${encodeURIComponent(step)}`),
  cancel: (id: string): Promise<void> => post(`/v1/runs/${encodeURIComponent(id)}/cancel`),
  pin: (id: string, pinned: boolean): Promise<void> => post(`/v1/runs/${encodeURIComponent(id)}/pin`, { pinned }),
  purge: (keep: number): Promise<PurgeRunsResponse> => post('/v1/runs/purge', { keep }),
  runSource: (req: RunFlowSourceRequest): Promise<Run> => post('/v1/runs/source', req),
};

// ---- memories ----

export const memories = {
  list: (params: MemoryQueryParams = {}): Promise<Memory[]> => orEmpty(get(`/v1/memories${buildQuery(params)}`)),
  search: (params: MemoryQueryParams = {}): Promise<ScoredMemory[]> => orEmpty(get(`/v1/memories/search${buildQuery(params)}`)),
  relevant: (subjects: Subject[], limit?: number): Promise<ScoredMemory[]> =>
    orEmpty(post('/v1/memories/relevant', { subjects, limit })),
  get: (id: string): Promise<Memory> => get(`/v1/memories/${encodeURIComponent(id)}`),
  create: (mem: Partial<Memory>): Promise<Memory> => post('/v1/memories', mem),
  patch: (id: string, mem: Partial<Memory>): Promise<Memory> => patch(`/v1/memories/${encodeURIComponent(id)}`, mem),
  delete: (id: string): Promise<void> => del(`/v1/memories/${encodeURIComponent(id)}`),
  promotion: (id: string): Promise<PromotionTarget> => get(`/v1/memories/${encodeURIComponent(id)}/promotion`),
  reindex: (): Promise<void> => post('/v1/memories/reindex'),
};

// ---- examples ----

export const examples = {
  list: (params: ExampleQueryParams = {}): Promise<SavedExample[]> => orEmpty(get(`/v1/examples${buildQuery(params)}`)),
  get: (id: string): Promise<SavedExample> => get(`/v1/examples/${encodeURIComponent(id)}`),
  create: (ex: Partial<SavedExample>): Promise<SavedExample> => post('/v1/examples', ex),
  update: (id: string, ex: Partial<SavedExample>): Promise<SavedExample> => put(`/v1/examples/${encodeURIComponent(id)}`, ex),
  delete: (id: string): Promise<void> => del(`/v1/examples/${encodeURIComponent(id)}`),
  fromRun: (req: ExampleFromRun): Promise<SavedExample> => post('/v1/examples/from-run', req),
  forOperations: (operationIDs: string[], limit?: number): Promise<SavedExample[]> =>
    orEmpty(get(`/v1/examples/for-operations${buildQuery({ op: operationIDs, limit })}`)),
  reindex: (): Promise<void> => post('/v1/examples/reindex'),
};

// ---- friction reports ----

// Friction reports are per machine, not per workspace (internal/server's
// friction handlers read the store directly): an agent files one about
// Sapien itself from whichever workspace it happens to be in, and a human
// reviews the queue as a whole. `send` and `drop` return the updated/void
// result; the caller (FrictionPage) reloads its own list afterwards.
export const friction = {
  list: (): Promise<FrictionReport[]> => orEmpty(get('/v1/friction')),
  preview: (id: string): Promise<FrictionPreview> => get(`/v1/friction/${encodeURIComponent(id)}/preview`),
  send: (id: string): Promise<FrictionReport> => post(`/v1/friction/${encodeURIComponent(id)}/send`),
  drop: (id: string): Promise<void> => del(`/v1/friction/${encodeURIComponent(id)}`),
};

// ---- context ----

export function buildContext(req: ContextRequest): Promise<ContextBundle> {
  return post('/v1/context', req);
}

// ---- environments / secrets ----

export const environments = {
  // Top-level array response, same as every other list endpoint above:
  // ListEnvironments (internal/workspace/environment.go) explicitly returns
  // a nil slice when environments/ is empty or missing, which Go marshals
  // as a bare top-level `null` -- normalizeNulls only rewrites null *inside*
  // an object by key, so a null response body still needs orEmpty here.
  list: (): Promise<Environment[]> => orEmpty(get('/v1/environments')),
  get: (name: string): Promise<Environment> => get(`/v1/environments/${encodeURIComponent(name)}`),
  getDefault: (): Promise<DefaultEnvironmentResponse> => get('/v1/environments/default'),
  setDefault: (name: string): Promise<void> => put('/v1/environments/default', { name }),
};

export const secrets = {
  list: (): Promise<string[]> => orEmpty(get('/v1/secrets')),
  set: (name: string, value: string): Promise<void> => put(`/v1/secrets/${encodeURIComponent(name)}`, { value }),
  delete: (name: string): Promise<void> => del(`/v1/secrets/${encodeURIComponent(name)}`),
};

// ---- events ----

// ---- workspaces ----

export const workspacesApi = {
  list: (): Promise<WorkspaceInfo[]> => orEmpty(get('/v1/workspaces')),
  register: (dir: string): Promise<WorkspaceInfo> => post('/v1/workspaces', { dir }),
};

export function getRecentEvents(limit = 200): Promise<Event[]> {
  return orEmpty(get(`/v1/events/recent${buildQuery({ limit })}`));
}
