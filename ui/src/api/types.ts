// Hand-mirrored from internal/domain/*.go JSON tags. Field names and
// optionality match the Go structs exactly (snake_case as emitted). Where a
// Go field is `omitempty` on a zero value, the TS field is optional here.
//
// Keep this file in sync with internal/domain when the Go side changes;
// there is no code generation step (the engine is the only source of
// truth and this is a hand-maintained mirror, same spirit as the CLI's own
// JSON consumers).

// ---- service.go ----

export type SourceKind = 'local' | 'git';

export interface Source {
  type: SourceKind;
  path?: string;
  url?: string;
  ref?: string;
  subdir?: string;
  contract?: string;
}

export type SyncStatus = 'pending' | 'ok' | 'error';

export interface EnvHint {
  base_url: string;
}

export interface AcceptedWarning {
  code: string;
  match?: string;
  reason: string;
}

export interface SourceLoc {
  file: string;
  pointer?: string;
  line?: number;
  column?: number;
}

export interface LintWarning {
  code: string;
  message: string;
  source?: SourceLoc;
}

export interface AcceptedLintWarning extends LintWarning {
  reason: string;
}

export interface Service {
  id: string;
  name: string;
  description?: string;
  owners?: string[];
  concepts?: string[];
  tasks?: Task[];
  source: Source;
  package_dir: string;
  contract_files?: string[];
  environments?: Record<string, EnvHint>;
  status: SyncStatus;
  error?: string;
  warnings?: LintWarning[];
  accepted_warnings?: AcceptedLintWarning[];
  task_coverage?: TaskCoverage;
  last_indexed?: string;
  commit?: string;
  /** Which source this machine reads the service from (PLAN §7b). */
  binding?: ServiceBinding;
  operation_count: number;
}

/** Where this machine reads a service from: a local checkout or the team's committed git source. */
export type BindingMode = 'local' | 'team';

/** A local git checkout of a service and what git says about it (read-only queries). */
export interface LocalCheckout {
  path: string;
  branch?: string;
  commit?: string;
  remote?: string;
  /** Modified and untracked files under the package dir: work that exists here and nowhere else yet. */
  dirty?: number;
  /** HEAD's commit time (RFC3339): separates a checkout worked in recently from an old backup clone. */
  committed_at?: string;
  /** Commits ahead/behind the team's ref (origin/<ref>) as of this clone's last fetch; 0 when the ref is unknown here. */
  ahead?: number;
  behind?: number;
  /** A `git worktree` checkout rather than a full clone. */
  worktree?: boolean;
  /** The API package directory discovered under Path; absent when none (not indexable yet). */
  package?: string;
}

/**
 * ServiceBinding says which source a service is read from on this machine
 * ("listening to") and what it could be read from instead ("can listen
 * to"). `team` is the committed git source in both modes; `local` is set in
 * local mode. `writable` is false for the managed clone of a git source.
 */
export interface ServiceBinding {
  mode: BindingMode;
  team?: Source;
  local?: LocalCheckout;
  writable: boolean;
}

export interface TaskTarget {
  operation: string;
  when?: string;
}

export interface TaskTest {
  query: string;
  expect_any: string[];
  top_k?: number;
}

export interface Task {
  id: string;
  phrases: string[];
  targets: TaskTarget[];
  tests?: TaskTest[];
}

export interface TaskCoverage {
  tasks: number;
  assertions: number;
  discoverable: number;
}

// ---- operation.go ----

export type Protocol = 'http';

export interface HTTPBinding {
  method: string;
  path: string;
}

export type ParamLocation = 'path' | 'query' | 'header' | 'cookie';

export interface Schema {
  kind: SchemaKind;
  name?: string;
  ref?: string;
  description?: string;
  properties?: Record<string, Schema>;
  property_order?: string[];
  required?: string[];
  additional_properties?: Schema;
  items?: Schema;
  variants?: Schema[];
  enum?: unknown[];
  format?: string;
  nullable?: boolean;
  example?: unknown;
  default?: unknown;
  read_only?: boolean;
  write_only?: boolean;
  deprecated?: boolean;
}

export type SchemaKind =
  | 'object'
  | 'array'
  | 'string'
  | 'number'
  | 'integer'
  | 'boolean'
  | 'null'
  | 'oneOf'
  | 'anyOf'
  | 'allOf'
  | 'ref'
  | 'any';

export interface Param {
  name: string;
  in: ParamLocation;
  required: boolean;
  description?: string;
  schema?: Schema;
  example?: unknown;
  deprecated?: boolean;
}

export interface OpExample {
  name?: string;
  summary?: string;
  value: unknown;
}

export interface Body {
  content_type: string;
  required: boolean;
  description?: string;
  schema?: Schema;
  examples?: OpExample[];
}

export interface Response {
  status: string;
  description?: string;
  content_type?: string;
  schema?: Schema;
  headers?: Record<string, Schema>;
  examples?: OpExample[];
}

export interface SecurityRequirement {
  scheme: string;
  type: string;
  scopes?: string[];
}

export interface Operation {
  id: string;
  service_id: string;
  protocol: Protocol;
  http?: HTTPBinding;
  raw_operation_id?: string;
  synthesized?: boolean;
  summary?: string;
  description?: string;
  tags?: string[];
  concepts?: string[];
  params?: Param[];
  request_body?: Body;
  responses?: Response[];
  security?: SecurityRequirement[];
  deprecated?: boolean;
  source: SourceLoc;
  hash: string;
}

export interface Field {
  operation_id: string;
  path: string;
  type: string;
  description?: string;
  required?: boolean;
  enum?: unknown[];
  format?: string;
}

export interface NamedSchema {
  service_id: string;
  name: string;
  hash: string;
  schema: Schema;
  used_by?: string[];
}

// ---- doc.go ----

export type DocSource = 'file' | 'contract_info' | 'contract_tag';

export type RefKind = 'operation' | 'path' | 'schema' | 'field' | 'concept' | 'service';

export interface DocRef {
  kind: RefKind;
  value: string;
}

export interface DocSection {
  id: string;
  ord: number;
  heading: string;
  level: number;
  body: string;
  refs?: DocRef[];
}

export interface Doc {
  id: string;
  service_id: string;
  path: string;
  title: string;
  source: DocSource;
  hash: string;
  sections?: DocSection[];
}

// ---- flow.go ----

export interface InputSpec {
  type?: string;
  required?: boolean;
  default?: unknown;
  description?: string;
}

export interface ExplicitParams {
  path?: Record<string, unknown>;
  query?: Record<string, unknown>;
  headers?: Record<string, string>;
}

export interface Poll {
  interval?: string;
  timeout?: string;
}

export interface Range {
  lt?: number;
  lte?: number;
  gt?: number;
  gte?: number;
}

export interface Assertion {
  expr?: string;
  status?: number;
  latency_ms?: Range;
  schema?: string;
  path?: string;
  eq?: unknown;
  neq?: unknown;
  exists?: boolean;
  matches?: string;
  contains?: unknown;
  message?: string;
  line?: number;
}

export interface Step {
  id: string;
  call: string;
  example?: string;
  input?: Record<string, unknown>;
  params?: ExplicitParams;
  body?: unknown;
  headers?: Record<string, string>;
  extract?: Record<string, string>;
  assert?: Assertion[];
  until?: string;
  poll?: Poll;
  timeout?: string;
  line?: number;
}

/**
 * The flow tier ladder (domain.FlowOwnerLocal/Workspace/Service): local is
 * this machine only (<workspace>/local/flows), workspace is the team's git
 * repo (<workspace>/flows), service is the owning repo's api/flows (owner_id
 * names the service). Kept as a plain string on Flow/FlowSummary because the
 * daemon also emits "" for pre-tier files; see pages/flows/tier.tsx.
 */
export type FlowOwnerKind = 'local' | 'workspace' | 'service';

export interface Flow {
  version: number;
  id: string;
  /** Raw YAML as stored; carried by GET /v1/flows/{id}. */
  source?: string;
  name?: string;
  description?: string;
  tags?: string[];
  inputs?: Record<string, InputSpec>;
  steps: Step[];
  path?: string;
  /** A FlowOwnerKind; "" or absent for flows written before tiers existed. */
  owner_kind?: string;
  owner_id?: string;
}

/**
 * How far a workspace-tier flow's file has travelled towards the team, from
 * a read-only git status of the workspace repo: untracked (never added),
 * modified (tracked, uncommitted changes), unpushed (committed, not on the
 * remote yet), shipped (committed and on the upstream).
 */
export type ShipStatus = 'untracked' | 'modified' | 'unpushed' | 'shipped';

export interface FlowSummary {
  id: string;
  name?: string;
  path: string;
  /** A FlowOwnerKind; "" for flows written before tiers existed. */
  owner_kind: string;
  owner_id?: string;
  tags?: string[];
  operations?: string[];
  step_count: number;
  hash: string;
  updated: string;
  /** Only for workspace-tier flows; absent for local/service tiers or when the workspace isn't in git. */
  shipped?: ShipStatus;
}

export type Severity = 'error' | 'warning';

export interface Diagnostic {
  code: string;
  severity: Severity;
  message: string;
  line?: number;
  column?: number;
  step_id?: string;
  suggestions?: string[];
}

export interface ValidationResult {
  valid: boolean;
  diagnostics?: Diagnostic[];
}

// ---- run.go ----

export type RunStatus = 'queued' | 'running' | 'passed' | 'failed' | 'errored' | 'cancelled';

export type StepStatus =
  | 'pending'
  | 'resolving'
  | 'requesting'
  | 'polling'
  | 'asserting'
  | 'passed'
  | 'failed'
  | 'errored'
  | 'skipped'
  | 'cancelled';

export interface ErrorInfo {
  code: string;
  message: string;
  details?: Record<string, unknown>;
}

export interface RunSummary {
  steps_total: number;
  steps_passed: number;
  steps_failed: number;
  steps_errored: number;
  steps_skipped: number;
  assertions: number;
  assertions_failed: number;
}

export interface RequestRecord {
  method: string;
  url: string;
  headers?: Record<string, string>;
  body?: unknown;
  body_raw?: string;
}

export interface ResponseRecord {
  status: number;
  headers?: Record<string, string>;
  body?: unknown;
  body_raw?: string;
  truncated?: boolean;
  size: number;
}

export interface Timings {
  dns_ms: number;
  connect_ms: number;
  tls_ms: number;
  ttfb_ms: number;
  total_ms: number;
}

export interface AssertionResult {
  expr: string;
  passed: boolean;
  actual?: unknown;
  message?: string;
  error?: string;
}

export interface StepResult {
  step_id: string;
  index: number;
  operation?: string;
  status: StepStatus;
  attempts?: number;
  request?: RequestRecord;
  response?: ResponseRecord;
  timings?: Timings;
  assertions?: AssertionResult[];
  out?: Record<string, unknown>;
  error?: ErrorInfo;
  started?: string;
  finished?: string;
}

export interface Run {
  id: string;
  flow_id?: string;
  flow_snapshot?: string;
  environment: string;
  inputs?: Record<string, unknown>;
  status: RunStatus;
  started: string;
  finished?: string;
  duration_ms?: number;
  steps?: StepResult[];
  error?: ErrorInfo;
  summary: RunSummary;
  pinned?: boolean;
  trigger?: string;
  operation_hashes?: Record<string, string>;
}

export interface RunFilter {
  flow?: string;
  status?: RunStatus;
  operation?: string;
  limit?: number;
  offset?: number;
}

// ---- memory.go ----

export type MemoryType =
  | 'note'
  | 'semantic'
  | 'behavioral'
  | 'testing'
  | 'invariant'
  | 'environment'
  | 'gotcha';

export type MemoryScope = 'personal' | 'workspace' | 'service' | 'flow';

export type MemoryStatus = 'active' | 'promoted' | 'superseded' | 'deprecated' | 'disputed';

export interface ErrorRef {
  operation?: string;
  status?: number;
  code?: string;
}

export interface Subject {
  service?: string;
  operation?: string;
  field?: string;
  schema?: string;
  flow?: string;
  step?: string;
  run?: string;
  environment?: string;
  error?: ErrorRef;
  concept?: string;
}

export interface ResolvedSubject {
  method?: string;
  path?: string;
  operation_hash?: string;
  resolved_at?: string;
  unresolved?: boolean;
}

export interface MemorySource {
  kind: string; // user | agent | run | import | documentation
  client?: string;
  model?: string;
  run_id?: string;
  step_id?: string;
  path?: string;
}

export interface Memory {
  id: string;
  type: MemoryType;
  scope: MemoryScope;
  subject: Subject;
  tags?: string[];
  source: MemorySource;
  status: MemoryStatus;
  created: string;
  updated: string;
  resolved?: ResolvedSubject;
  text: string;
  file_path?: string;
  hash?: string;
}

export interface MemoryQueryParams {
  q?: string;
  scope?: MemoryScope;
  type?: MemoryType;
  service?: string;
  op?: string;
  flow?: string;
  limit?: number;
  min_score?: number;
}

export interface ScoredMemory {
  memory: Memory;
  score: number;
  reasons?: string[];
}

// ---- example.go ----

export type ExampleScope = 'workspace' | 'service';

export interface ExampleExpect {
  status?: number;
  body?: unknown;
}

export interface ExampleVerified {
  env?: string;
  run?: string;
  step?: string;
  at: string;
  by?: MemorySource;
}

export interface SavedExample {
  version: number;
  id: string;
  operation: string;
  description?: string;
  scope: ExampleScope;
  service: string;
  input?: Record<string, unknown>;
  body?: unknown;
  headers?: Record<string, string>;
  expect?: ExampleExpect;
  verified?: ExampleVerified;
  tags?: string[];
  created: string;
  updated: string;
  path?: string;
}

// RequestExample is the daemon's answer to "what does a call to this
// operation look like?": a ready-to-send payload plus where it came from.
// Resolved server-side (internal/example.Resolve) so this page, get_api, and
// the CLI never disagree about which example wins.
export type RequestExampleSource = 'verified' | 'saved' | 'contract' | 'schema';

export interface RequestExample {
  operation: string;
  source: RequestExampleSource;
  source_id?: string;
  input?: Record<string, unknown>;
  body?: unknown;
  headers?: Record<string, string>;
  note?: string;
}

export interface ExampleQueryParams {
  operation?: string;
  service?: string;
  tag?: string;
  text?: string;
  limit?: number;
}

export interface ExampleFromRun {
  run_id: string;
  step_id?: string;
  id: string;
  description?: string;
  scope?: ExampleScope;
  tags?: string[];
  source?: MemorySource;
}

// ---- environment.go ----

export type AuthType = 'none' | 'bearer' | 'header' | 'basic' | 'query';

export interface Auth {
  type: AuthType;
  token?: string;
  name?: string;
  value?: string;
  username?: string;
  password?: string;
}

export interface ServiceEnv {
  base_url?: string;
  auth?: Auth;
}

export interface Transport {
  timeout_ms?: number;
  connect_timeout_ms?: number;
  insecure_tls?: boolean;
  proxy?: string;
  max_redirects?: number;
  max_body_bytes?: number;
}

export interface Redaction {
  headers?: string[];
  json_paths?: string[];
}

export interface Environment {
  version: number;
  name: string;
  production: boolean;
  services?: Record<string, ServiceEnv>;
  vars?: Record<string, string>;
  auth?: Record<string, Auth>;
  transport?: Transport;
  redaction?: Redaction;
  path?: string;
}

// ---- events.go ----

export type EventType =
  | 'catalog.changed'
  | 'service.sync_failed'
  | 'run.started'
  | 'run.step'
  | 'run.finished'
  | 'memory.created'
  | 'memory.changed'
  | 'flow.changed';

export interface Event {
  type: EventType;
  time: string;
  payload?: unknown;
}

export interface CatalogChange {
  service: string;
  added?: string[];
  removed?: string[];
  changed?: string[];
}

// ---- search.go ----

export interface SearchResult {
  operation: Operation;
  score: number;
  matched_on?: string[];
  tasks?: Array<{ id: string; phrase?: string; when?: string }>;
}

export interface DocSearchResult {
  service: string;
  doc_id: string;
  path: string;
  title: string;
  section_id: string;
  heading: string;
  snippet: string;
  refs?: DocRef[];
  score: number;
}

export interface SearchOptions {
  service?: string;
  method?: string;
  limit?: number;
  include_deprecated?: boolean;
}

export interface ContextRequest {
  intent: string;
  operations?: string[];
  flow?: string;
  environment?: string;
  budget_tokens?: number;
}

export type Tier = 'contract' | 'documentation' | 'memory' | 'run';

export interface OperationContext {
  tier: Tier;
  id: string;
  method: string;
  path: string;
  summary?: string;
  description?: string;
  params?: string[];
  body?: string[];
  response?: string[];
  security?: string[];
  examples?: unknown[];
  score?: number;
}

export interface DocContext {
  tier: Tier;
  service: string;
  path: string;
  heading: string;
  body: string;
  truncated?: boolean;
  uri: string;
}

export interface MemoryContext {
  tier: Tier;
  id: string;
  type: MemoryType;
  scope: MemoryScope;
  source: string;
  subject: Subject;
  text: string;
  score?: number;
}

export interface ExampleContext {
  id: string;
  operation: string;
  description?: string;
  verified: boolean;
  env?: string;
  input?: Record<string, unknown>;
  body?: unknown;
  tags?: string[];
}

export interface FlowContext {
  id: string;
  name?: string;
  steps: string[];
}

export interface RunContext {
  tier: Tier;
  id: string;
  flow_id?: string;
  status: string;
  summary: string;
}

export interface ContextBundle {
  intent: string;
  operations: OperationContext[];
  docs: DocContext[];
  examples?: ExampleContext[];
  memories: MemoryContext[];
  flows: FlowContext[];
  runs: RunContext[];
  omitted?: Record<string, number>;
  estimated_tokens: number;
}

// ---- workspace.go ----

export interface ServiceRef {
  name: string;
  source: Source;
  /** The committed source when a per-machine override replaced `source`. */
  team?: Source;
}

export interface Workspace {
  version: number;
  name: string;
  services?: ServiceRef[];
  default_environment?: string;
  dir: string;
  file: string;
}

// ---- engine.go wire shapes (internal/server/wire.go, internal/engine/engine.go) ----

export interface RunOptionsWire {
  environment?: string;
  inputs?: Record<string, unknown>;
  continue_on_failure?: boolean;
  allow_production?: boolean;
  trigger?: string;
}

export interface CallRequest {
  operation: string;
  params?: Record<string, unknown>;
  body?: unknown;
  headers?: Record<string, string>;
  env: string;
  allow_production?: boolean;
  trigger?: string;
}

export interface AddServiceRequest {
  name: string;
  source: Source;
}

export interface FlowYAMLRequest {
  yaml: string;
  path?: string;
}

/** POST /v1/flows: FlowYAMLRequest plus the tier to create the file in (omitted keeps the daemon's default). */
export interface CreateFlowRequest extends FlowYAMLRequest {
  owner_kind?: FlowOwnerKind;
  owner_id?: string;
}

/** POST /v1/flows/{id}/rescope: move a flow between tiers; owner_id names the service for `service`. */
export interface RescopeFlowRequest {
  owner_kind: FlowOwnerKind;
  owner_id?: string;
  /** Also `git add` + `git commit` the moved file in the workspace repo (never a push). Refused with 400 for any tier but `workspace`. */
  commit?: boolean;
  /** Commit message; daemon default when omitted. */
  message?: string;
}

/** GET /v1/services/{name}/binding (engine.BindingInfo). */
export interface BindingInfo {
  service: string;
  binding: ServiceBinding;
  /** Local checkouts of the same repository found on this machine: one-click bind targets. */
  candidates?: LocalCheckout[];
}

/** PUT /v1/services/{name}/binding. */
export interface BindServiceRequest {
  path: string;
  /** Bypass the origin/package refusal: bind a fork/mirror, or a checkout with no API package yet. */
  force?: boolean;
}

/** GET /v1/services/{name}/checkouts?path=<dir> -> one directory level, for the checkout picker. */
export interface DirListing {
  path: string;
  /** Absent at the filesystem root. */
  parent?: string;
  entries: DirEntry[];
}

/** One subdirectory in a DirListing. `checkout` is set when it is the root of a git repository. */
export interface DirEntry {
  name: string;
  path: string;
  checkout?: LocalCheckout;
  /** Its origin names this service's team repository. */
  matches: boolean;
  /** Why not (another repository's origin), or "no API package under this checkout" when matches but not indexable. */
  reason?: string;
}

/** POST /v1/services/from-checkout: registers a local checkout's origin as the team's git source and binds it here. */
export interface AddFromCheckoutRequest {
  name?: string;
  path: string;
  ref?: string;
  force?: boolean;
  /** Accept a path that is a subdirectory of its repository (a monorepo service) instead of refusing it. */
  allow_subdir?: boolean;
}

export interface RunFlowSourceRequest {
  yaml: string;
  opts: RunOptionsWire;
}

export interface PromotionTarget {
  kind: string; // "openapi" | "doc"
  file: string;
  line?: number;
  pointer?: string;
  section?: string;
  current?: string;
  memory: Memory;
  suggested?: string;
}

export interface HealthResponse {
  ok: boolean;
  version: string;
  workspace: string;
}

export interface DefaultEnvironmentResponse {
  name: string;
}

export interface PurgeRunsResponse {
  removed: number;
}

// ---- friction.go (internal/friction) ----

// The friction-report loop (PLAN.md): an MCP client files a report with
// report_friction, it is queued on disk under ~/.sapien/friction, and a
// human reviews the queue here (or with `sapien friction list/show`)
// before `send` posts it as a GitHub Discussion through the `gh` CLI.
// Nothing is ever sent automatically.
export type FrictionCategory = 'bug' | 'idea' | 'docs' | 'missing';

export interface FrictionReport {
  id: string; // "fr_..."
  title: string;
  category: FrictionCategory;
  tool?: string; // the Sapien tool or command involved
  tried?: string; // what the agent was trying to do
  happened: string; // what happened instead
  would_help?: string; // what would have helped
  workspace?: string; // workspace name
  client?: string; // MCP client name, or "cli"
  version?: string; // Sapien version at report time
  created: string; // RFC3339
  status: 'pending' | 'sent';
  sent_url?: string;
  sent_at?: string;
  path?: string; // file on disk
}

/**
 * GET /v1/friction/{id}/preview's answer: the exact Discussion title and
 * Markdown body that `send` would post, and which repo/category it would
 * post to -- what the human reviews before anything goes public.
 */
export interface FrictionPreview {
  title: string;
  body: string;
  repo: string;
  category: string;
}

// ---- errors (internal/errs) ----

export interface ApiError {
  code: string;
  message: string;
  details?: Record<string, unknown>;
  source?: SourceLoc;
  hint?: string;
}
