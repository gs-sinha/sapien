// Regression coverage for the "null sweep" (build brief item 2): every
// array-typed field in api/types.ts that's `omitempty` on the Go side can
// legitimately be absent, or -- for the handful of non-omitempty fields
// normalizeNulls exists for -- an explicit JSON `null`. This test mocks
// '../api/client' directly (bypassing the real client.ts, so normalizeNulls
// never runs) and feeds every page in the brief a response shaped exactly
// that way, to prove each page's own `|| []` / `?? []` guards -- not just
// the client-level normalizer -- hold up on their own.
import { render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { describe, expect, it, vi } from 'vitest';
import type { ReactElement } from 'react';
import FlowsPage from '../pages/FlowsPage';
import FlowDetailPage from '../pages/FlowDetailPage';
import RunsPage from '../pages/RunsPage';
import RunDetailPage from '../pages/RunDetailPage';
import ServicesPage from '../pages/ServicesPage';
import ServiceDetailPage from '../pages/ServiceDetailPage';
import OperationsPage from '../pages/OperationsPage';
import OperationDetailPage from '../pages/OperationDetailPage';
import ExamplesPage from '../pages/ExamplesPage';
import MemoriesPage from '../pages/MemoriesPage';
import EventsPage from '../pages/EventsPage';

// Every array below is `null` (never simply omitted, which TypeScript would
// accept for an optional field without a cast) to stand in for both cases:
// a real omitempty field is just missing from the parsed object, but a
// handful of non-omitempty fields (Flow.steps, ContextBundle's tiers,
// FlowContext.steps) come back as a literal JSON null, which is exactly
// what a page's own guard has to survive too, not only normalizeNulls.
const nullyFlowSummary = {
  id: 'f1',
  path: 'flows/f1.flow.yaml',
  owner_kind: 'workspace',
  step_count: 0,
  hash: 'h1',
  updated: '2026-01-01T00:00:00Z',
  tags: null,
  operations: null,
};

const nullyFlow = {
  version: 1,
  id: 'f1',
  steps: null,
  tags: null,
  inputs: null,
  source: 'version: 1\nid: f1\nsteps: []\n',
};

const nullyRun = {
  id: 'run_1',
  environment: 'stage',
  status: 'passed',
  started: '2026-01-01T00:00:00Z',
  summary: { steps_total: 0, steps_passed: 0, steps_failed: 0, steps_errored: 0, steps_skipped: 0, assertions: 0, assertions_failed: 0 },
  steps: null,
};

const nullyService = {
  id: 'orders',
  name: 'orders',
  source: { type: 'local', path: '/repo/orders' },
  package_dir: '/repo/orders/api',
  status: 'ok',
  operation_count: 0,
  owners: null,
  concepts: null,
  contract_files: null,
  warnings: null,
  accepted_warnings: null,
  environments: null,
};

const nullyOperation = {
  id: 'orders.createOrder',
  service_id: 'orders',
  protocol: 'http',
  http: { method: 'POST', path: '/v1/orders' },
  source: { file: 'contract.yaml' },
  hash: 'h1',
  tags: null,
  concepts: null,
  params: null,
  responses: null,
  security: null,
};

const nullyExample = {
  version: 1,
  id: 'ex1',
  operation: 'orders.createOrder',
  scope: 'workspace',
  service: 'orders',
  created: '2026-01-01T00:00:00Z',
  updated: '2026-01-01T00:00:00Z',
  tags: null,
};

const nullyMemory = {
  id: 'mem_1',
  type: 'note',
  scope: 'workspace',
  subject: {},
  source: { kind: 'user' },
  status: 'active',
  created: '2026-01-01T00:00:00Z',
  updated: '2026-01-01T00:00:00Z',
  text: 'a memory',
  tags: null,
};

// ServiceDetailPage casts docs.list()'s result straight to Doc[] (see its
// own comment on that call), so this is shaped like a Doc, not the
// DocSearchResult the endpoint can also return.
const nullyDoc = {
  id: 'orders/docs/allocation.md',
  service_id: 'orders',
  path: 'docs/allocation.md',
  title: 'Allocation',
  source: 'file',
  hash: 'h1',
  sections: null,
};

vi.mock('../api/client', () => ({
  flows: {
    list: vi.fn(async () => [nullyFlowSummary]),
    get: vi.fn(async () => nullyFlow),
    validate: vi.fn(async () => ({ valid: true, diagnostics: null })),
  },
  runs: {
    list: vi.fn(async () => [nullyRun]),
    get: vi.fn(async () => nullyRun),
  },
  services: {
    list: vi.fn(async () => [nullyService]),
    get: vi.fn(async () => nullyService),
    sync: vi.fn(async () => [nullyService]),
  },
  operations: {
    search: vi.fn(async () => [nullyOperation]),
    get: vi.fn(async () => nullyOperation),
    // Unlike the other array-typed fields here, GET /v1/operations/{id}/fields
    // is a top-level list endpoint the real client always wraps in orEmpty
    // (see client.ts), so it's never actually null by the time a page sees
    // it -- kept as a real (empty) array here rather than manufacturing a
    // scenario the client layer already rules out.
    fields: vi.fn(async () => []),
  },
  docs: {
    list: vi.fn(async () => [nullyDoc]),
    get: vi.fn(async () => ({
      id: 'orders/docs/allocation.md',
      service_id: 'orders',
      path: 'docs/allocation.md',
      title: 'Allocation',
      source: 'file',
      hash: 'h1',
      sections: null,
    })),
  },
  findDocSection: vi.fn(() => undefined),
  examples: {
    list: vi.fn(async () => [nullyExample]),
  },
  memories: {
    list: vi.fn(async () => [nullyMemory]),
    search: vi.fn(async () => [{ memory: nullyMemory, score: 1, reasons: null }]),
  },
  environments: {
    list: vi.fn(async () => [{ version: 1, name: 'stage', production: false }]),
    getDefault: vi.fn(async () => ({ name: 'stage' })),
  },
  buildContext: vi.fn(async () => ({
    intent: 'allocate a rider now',
    operations: null,
    docs: null,
    examples: null,
    memories: null,
    flows: null,
    runs: null,
    estimated_tokens: 0,
  })),
  getRecentEvents: vi.fn(async () => []),
}));

vi.mock('../api/flowsExtra', () => ({
  getRunHints: vi.fn(async () => []),
}));

function withRouter(path: string, routePath: string, element: ReactElement) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path={routePath} element={element} />
      </Routes>
    </MemoryRouter>,
  );
}

describe('pages tolerate null (or missing) array fields', () => {
  it('FlowsPage', async () => {
    withRouter('/ui/flows', '/ui/flows', <FlowsPage />);
    await waitFor(() => expect(screen.getByRole('heading', { name: 'Flows' })).toBeInTheDocument());
    expect(await screen.findByText('f1')).toBeInTheDocument();
  });

  it('FlowDetailPage', async () => {
    withRouter('/ui/flows/f1', '/ui/flows/:id', <FlowDetailPage />);
    await waitFor(() => expect(screen.getByText('No steps.')).toBeInTheDocument());
  });

  it('RunsPage', async () => {
    withRouter('/ui/runs', '/ui/runs', <RunsPage />);
    await waitFor(() => expect(screen.getByRole('heading', { name: 'Runs' })).toBeInTheDocument());
    expect(await screen.findByText('run_1')).toBeInTheDocument();
  });

  it('RunDetailPage', async () => {
    withRouter('/ui/runs/run_1', '/ui/runs/:id', <RunDetailPage />);
    await waitFor(() => expect(screen.getByText('run_1')).toBeInTheDocument());
    expect(await screen.findByText('No steps recorded.')).toBeInTheDocument();
  });

  it('ServicesPage', async () => {
    withRouter('/ui/services', '/ui/services', <ServicesPage />);
    await waitFor(() => expect(screen.getByRole('heading', { name: 'Services' })).toBeInTheDocument());
    expect(await screen.findByText('orders')).toBeInTheDocument();
  });

  it('ServiceDetailPage', async () => {
    withRouter('/ui/services/orders', '/ui/services/:name', <ServiceDetailPage />);
    await waitFor(() => expect(screen.getByRole('heading', { name: 'orders' })).toBeInTheDocument());
  });

  it('OperationsPage in keyword mode', async () => {
    withRouter('/ui/operations?q=order', '/ui/operations', <OperationsPage />);
    await waitFor(() => expect(screen.getByRole('heading', { name: 'Operations' })).toBeInTheDocument());
    expect(await screen.findByText('orders.createOrder')).toBeInTheDocument();
  });

  it('OperationsPage in intent mode', async () => {
    withRouter('/ui/operations?intent=allocate+a+rider+now', '/ui/operations', <OperationsPage />);
    await waitFor(() => expect(screen.getByText(/No matching operations/)).toBeInTheDocument());
  });

  it('OperationDetailPage', async () => {
    withRouter('/ui/operations/orders.createOrder', '/ui/operations/:id', <OperationDetailPage />);
    await waitFor(() => expect(screen.getByRole('heading', { name: 'orders.createOrder' })).toBeInTheDocument());
  });

  it('ExamplesPage', async () => {
    withRouter('/ui/examples', '/ui/examples', <ExamplesPage />);
    await waitFor(() => expect(screen.getByRole('heading', { name: 'Examples' })).toBeInTheDocument());
    expect(await screen.findByText('ex1')).toBeInTheDocument();
  });

  it('MemoriesPage', async () => {
    withRouter('/ui/memories', '/ui/memories', <MemoriesPage />);
    await waitFor(() => expect(screen.getByRole('heading', { name: 'Memories' })).toBeInTheDocument());
    expect(await screen.findByText('mem_1')).toBeInTheDocument();
  });

  it('EventsPage', async () => {
    withRouter('/ui/events', '/ui/events', <EventsPage />);
    await waitFor(() => expect(screen.getByRole('heading', { name: 'Events' })).toBeInTheDocument());
    expect(screen.getByText('No events yet')).toBeInTheDocument();
  });
});
