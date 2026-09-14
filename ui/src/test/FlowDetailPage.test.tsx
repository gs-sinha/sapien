import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes, useParams } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import FlowDetailPage from '../pages/FlowDetailPage';
import { useEvents } from '../state/events';
import { useToasts } from '../state/toast';
import type { Environment, FlowSummary, Operation, Run, Service } from '../api/types';

const stageEnv: Environment = { version: 1, name: 'stage', production: false };
const prodEnv: Environment = { version: 1, name: 'prod', production: true };

const sampleSource = ['version: 1', 'id: qcom-order', 'steps:', '  - id: create', '    call: qcom.createOrder'].join('\n');

const createOrderOp: Operation = {
  id: 'qcom.createOrder',
  service_id: 'qcom',
  protocol: 'http',
  http: { method: 'POST', path: '/v1/orders' },
  params: [{ name: 'riderId', in: 'query', required: true }],
  source: { file: 'contract.yaml' },
  hash: 'h1',
};

const flowsGet = vi.fn(async (_id: string) => ({
  version: 1,
  id: 'qcom-order',
  name: 'QCOM order',
  description: 'Places and allocates an order',
  tags: ['qcom'],
  path: 'flows/qcom-order.flow.yaml',
  owner_kind: 'workspace',
  inputs: {},
  steps: [{ id: 'create', call: 'qcom.createOrder' }],
  source: sampleSource,
}));
const flowsUpdate = vi.fn(async (id: string, req: { yaml: string }) => ({
  version: 1,
  id,
  steps: [],
  source: req.yaml,
}));
const finishedRun: Run = {
  id: 'run_1',
  flow_id: 'qcom-order',
  environment: 'stage',
  status: 'passed',
  started: '2026-01-01T00:00:00Z',
  duration_ms: 120,
  steps: [{ step_id: 'create', index: 0, status: 'passed' }],
  summary: { steps_total: 1, steps_passed: 1, steps_failed: 0, steps_errored: 0, steps_skipped: 0, assertions: 2, assertions_failed: 0 },
};
// The daemon answers POST /run only once the run is over; the test resolves
// it by hand so the in-flight state (progress from the event stream) is
// observable, exactly as it is in the browser.
let releaseRun: (run: Run) => void = () => {};
const flowsRun = vi.fn(
  (_id: string, _opts: unknown): Promise<Run> =>
    new Promise<Run>((resolve) => {
      releaseRun = resolve;
    }),
);
const flowsRescope = vi.fn(async (id: string, ownerKind: string, ownerId?: string, _opts?: { commit?: boolean; message?: string }) => ({
  version: 1,
  id,
  steps: [],
  owner_kind: ownerKind,
  owner_id: ownerId,
}));
// GET /v1/flows/{id} carries no `shipped` (only FlowSummary does); the page
// looks it up via a second, best-effort flows.list(id) call. Empty by
// default so the default page has no ship badge to account for.
const flowsList = vi.fn(async (_q?: string): Promise<FlowSummary[]> => []);
const flowsCommit = vi.fn(async (_id: string, _message?: string): Promise<FlowSummary> => ({
  id: 'qcom-order',
  path: 'flows/qcom-order.flow.yaml',
  owner_kind: 'workspace',
  step_count: 1,
  hash: 'h',
  updated: '2026-01-05T00:00:00Z',
  shipped: 'unpushed',
}));
// No service is bound to a checkout unless a test says so, so the default
// page has exactly one <select> (the environment picker).
const servicesList = vi.fn(async (): Promise<Service[]> => []);
const qcomBound: Service = {
  id: 'qcom',
  name: 'qcom',
  status: 'ok',
  source: { type: 'git', url: 'git@github.com:acme/qcom.git' },
  package_dir: '/home/me/code/qcom/api',
  operation_count: 1,
  binding: { mode: 'local', team: { type: 'git', url: 'git@github.com:acme/qcom.git' }, local: { path: '/home/me/code/qcom' }, writable: true },
};
const environmentsList = vi.fn(async (): Promise<Environment[]> => [stageEnv]);
const environmentsGetDefault = vi.fn(async () => ({ name: 'stage' }));
const runsRunSource = vi.fn(
  async (_req: { yaml: string; opts: unknown }): Promise<Run> => ({
    id: 'run_1',
    environment: 'stage',
    status: 'passed',
    started: '2026-01-01T00:00:00Z',
    summary: { steps_total: 1, steps_passed: 1, steps_failed: 0, steps_errored: 0, steps_skipped: 0, assertions: 0, assertions_failed: 0 },
  }),
);

vi.mock('../api/client', () => ({
  flows: {
    get: (id: string) => flowsGet(id),
    list: (q?: string) => flowsList(q),
    commit: (id: string, message?: string) => flowsCommit(id, message),
    update: (id: string, req: { yaml: string }) => flowsUpdate(id, req),
    run: (id: string, opts: unknown) => flowsRun(id, opts),
    // Forwards a 4th arg only when the caller passed one, so a plain
    // 3-arg rescope call is recorded (and assertable) as exactly 3 args.
    rescope: (id: string, ownerKind: string, ownerId?: string, opts?: { commit?: boolean; message?: string }) =>
      opts !== undefined ? flowsRescope(id, ownerKind, ownerId, opts) : flowsRescope(id, ownerKind, ownerId),
    validate: vi.fn(async () => ({ valid: true, diagnostics: [] })),
  },
  services: {
    list: () => servicesList(),
  },
  operations: {
    get: vi.fn(async (id: string): Promise<Operation> => {
      if (id === 'qcom.createOrder') return createOrderOp;
      throw new Error('unknown operation');
    }),
  },
  examples: {
    create: vi.fn(),
  },
  environments: {
    list: () => environmentsList(),
    getDefault: () => environmentsGetDefault(),
  },
  runs: {
    list: vi.fn(async () => []),
    runSource: (req: { yaml: string; opts: unknown }) => runsRunSource(req),
  },
}));

function RunStub() {
  const { runId } = useParams();
  return <div>RUN PAGE {runId}</div>;
}

function renderPage() {
  return render(
    <MemoryRouter initialEntries={['/ui/flows/qcom-order']}>
      <Routes>
        <Route path="/ui/flows/:id" element={<FlowDetailPage />} />
        <Route path="/ui/runs/:runId" element={<RunStub />} />
      </Routes>
    </MemoryRouter>,
  );
}

beforeEach(() => {
  sessionStorage.clear();
  useToasts.setState({ toasts: [] });
});

afterEach(() => {
  environmentsList.mockClear();
  environmentsList.mockImplementation(async () => [stageEnv]);
  environmentsGetDefault.mockClear();
  environmentsGetDefault.mockImplementation(async () => ({ name: 'stage' }));
  flowsUpdate.mockClear();
  flowsRescope.mockClear();
  flowsList.mockClear();
  flowsList.mockImplementation(async () => []);
  flowsCommit.mockClear();
  servicesList.mockClear();
  servicesList.mockImplementation(async () => []);
  runsRunSource.mockClear();
  flowsRun.mockClear();
});

function flowAtTier(ownerKind: string, ownerId?: string) {
  return {
    version: 1,
    id: 'qcom-order',
    name: 'QCOM order',
    description: 'Places and allocates an order',
    tags: ['qcom'],
    path: ownerKind === 'local' ? 'local/flows/qcom-order.flow.yaml' : 'flows/qcom-order.flow.yaml',
    owner_kind: ownerKind,
    owner_id: ownerId,
    inputs: {},
    steps: [{ id: 'create', call: 'qcom.createOrder' }],
    source: sampleSource,
  };
}

// Pushes an already-summarized event through the store the same way an
// incoming WebSocket frame would, so the page's subscriptions fire.
function emit(e: Parameters<ReturnType<typeof useEvents.getState>['_append']>[0]) {
  act(() => {
    useEvents.getState()._append(e);
  });
}

describe('FlowDetailPage', () => {
  it('shows the steps and the run controls up front, with the YAML collapsed', async () => {
    const { container } = renderPage();

    await waitFor(() => expect(screen.getByText('QCOM order')).toBeInTheDocument());

    // The materialized step and the Run button are both on screen without
    // opening anything: they're what this page is for.
    expect(screen.getByText('create')).toBeInTheDocument();
    expect(screen.getByText('qcom.createOrder')).toBeInTheDocument();
    expect(await screen.findByRole('button', { name: 'Run' })).toBeInTheDocument();

    // The source is behind a collapsed disclosure that says how big it is.
    expect(container.textContent).not.toContain('id: qcom-order');
    expect(screen.getByRole('button', { name: /YAML/ })).toHaveTextContent('5 lines');

    fireEvent.click(screen.getByRole('button', { name: /YAML/ }));

    // Source YAML is rendered verbatim (line by line) rather than
    // reconstructed from the parsed fields. YamlView colours the "id" key
    // in its own <span>, so the line's full text is checked via the
    // container's textContent rather than getByText (which only concatenates
    // an element's direct text-node children, not text split across a
    // nested element -- see testing-library's own guidance on this).
    expect(container.textContent).toContain('id: qcom-order');
    expect(container.textContent).toContain('call: qcom.createOrder');
  });

  it('edits a step input, runs with edits, and saves the edits to the flow', async () => {
    const user = userEvent.setup();
    renderPage();

    await waitFor(() => expect(screen.getByText('QCOM order')).toBeInTheDocument());

    // Expand the step card.
    fireEvent.click(screen.getByText('create').closest('button')!);

    // The operation's declared, required "riderId" param shows up as a
    // suggestion chip once GET /v1/operations/qcom.createOrder resolves.
    const addRiderId = await screen.findByRole('button', { name: /riderId/ });
    await user.click(addRiderId);

    const valueInput = screen.getByPlaceholderText('value');
    await user.type(valueInput, 'rider_42');

    // The step and the Run button both reflect the pending edit.
    expect(screen.getByText('modified')).toBeInTheDocument();
    const runButton = await screen.findByRole('button', { name: 'Run with edits' });
    await waitFor(() => expect(runButton).toBeEnabled());

    await user.click(runButton);

    await waitFor(() => expect(runsRunSource).toHaveBeenCalledTimes(1));
    const [req] = runsRunSource.mock.calls[0] as [{ yaml: string; opts: unknown }];
    expect(req.yaml).toContain('riderId: rider_42');
    expect(req.yaml).toContain('call: qcom.createOrder');
    expect(req.opts).toEqual({ environment: 'stage', inputs: {}, allow_production: false, trigger: 'ui' });

    // Stays on the flow page and reports the finished run there, with a way
    // through to its details rather than a forced jump.
    await waitFor(() => expect(screen.getByText('Open run details →')).toBeInTheDocument());
    expect(screen.getByText('QCOM order')).toBeInTheDocument();
    expect(screen.queryByText('RUN PAGE run_1')).not.toBeInTheDocument();
    expect(screen.getByText('Open run details →')).toHaveAttribute('href', '/ui/runs/run_1');
  });

  it('blocks Run against a production environment until allow-production is ticked', async () => {
    environmentsList.mockImplementation(async () => [stageEnv, prodEnv]);
    environmentsGetDefault.mockImplementation(async () => ({ name: 'prod' }));
    const user = userEvent.setup();
    renderPage();

    await screen.findByText('This environment is marked production.');
    const runButton = screen.getByRole('button', { name: 'Run' });
    expect(runButton).toBeDisabled();

    await user.click(screen.getByRole('checkbox', { name: /allow running against production/i }));
    expect(runButton).toBeEnabled();

    await user.click(runButton);
    await waitFor(() => expect(flowsRun).toHaveBeenCalledTimes(1));
    expect(flowsRun.mock.calls[0][1]).toMatchObject({ environment: 'prod', allow_production: true });
    await act(async () => {
      releaseRun(finishedRun);
    });
  });

  it('drops the production tick when the environment changes', async () => {
    environmentsList.mockImplementation(async () => [stageEnv, prodEnv]);
    environmentsGetDefault.mockImplementation(async () => ({ name: 'prod' }));
    const user = userEvent.setup();
    renderPage();

    await screen.findByText('This environment is marked production.');
    await user.click(screen.getByRole('checkbox', { name: /allow running against production/i }));
    expect(screen.getByRole('button', { name: 'Run' })).toBeEnabled();

    // Away to stage (no notice at all), then back: the earlier unlock must
    // not still be in effect.
    await user.selectOptions(screen.getByRole('combobox'), 'stage');
    expect(screen.queryByText('This environment is marked production.')).not.toBeInTheDocument();

    await user.selectOptions(screen.getByRole('combobox'), 'prod');
    expect(screen.getByRole('checkbox', { name: /allow running against production/i })).not.toBeChecked();
    expect(screen.getByRole('button', { name: 'Run' })).toBeDisabled();
  });

  it('clamps a long agent-written description behind "Show more"', async () => {
    const long = `Long description. ${'Detail about the flow. '.repeat(20).trim()}`;
    flowsGet.mockImplementationOnce(async () => ({
      version: 1,
      id: 'qcom-order',
      name: 'QCOM order',
      description: long,
      tags: ['qcom'],
      path: 'flows/qcom-order.flow.yaml',
      owner_kind: 'workspace',
      inputs: {},
      steps: [{ id: 'create', call: 'qcom.createOrder' }],
      source: sampleSource,
    }));
    renderPage();

    const para = await screen.findByText(long);
    expect(para.className).toContain('line-clamp-2');

    fireEvent.click(screen.getByRole('button', { name: 'Show more' }));
    expect(screen.getByText(long).className).not.toContain('line-clamp-2');

    fireEvent.click(screen.getByRole('button', { name: 'Show less' }));
    expect(screen.getByText(long).className).toContain('line-clamp-2');
  });

  it('keeps a short description unclamped and offers no toggle', async () => {
    renderPage();
    const para = await screen.findByText('Places and allocates an order');
    expect(para.className).not.toContain('line-clamp-2');
    expect(screen.queryByRole('button', { name: 'Show more' })).not.toBeInTheDocument();
  });

  it('follows a run in place: step progress from the event stream, then the result', async () => {
    const user = userEvent.setup();
    renderPage();

    await waitFor(() => expect(screen.getByText('QCOM order')).toBeInTheDocument());
    const runButton = await screen.findByRole('button', { name: 'Run' });
    await waitFor(() => expect(runButton).toBeEnabled());
    await user.click(runButton);

    await waitFor(() => expect(flowsRun).toHaveBeenCalledTimes(1));
    expect(flowsRun.mock.calls[0][1]).toEqual({ environment: 'stage', inputs: {}, allow_production: false, trigger: 'ui' });

    // POST /run hasn't answered yet: the run is only visible through events.
    // run.started names the id, so the link to the run's own page works
    // while the run is still going.
    emit({ type: 'run.started', time: 't1', status: 'running', summary: '', ids: { run_id: 'run_1', flow_id: 'qcom-order' } });
    expect(screen.getByText('Open run details →')).toHaveAttribute('href', '/ui/runs/run_1');

    emit({ type: 'run.step', time: 't2', status: 'requesting', summary: '', ids: { run_id: 'run_1', step_id: 'create' } });
    // The status shows both on the progress line and on the step's own card.
    expect(screen.getByText(/0\/1 steps/)).toHaveTextContent('create');
    const stepRow = screen.getByText('create').closest('button')!;
    expect(within(stepRow).getByText('requesting')).toBeInTheDocument();

    emit({ type: 'run.step', time: 't3', status: 'passed', summary: '', ids: { run_id: 'run_1', step_id: 'create' } });
    expect(screen.getByText(/1\/1 steps/)).toBeInTheDocument();

    // A step of some other run never leaks into this page's progress.
    emit({ type: 'run.step', time: 't4', status: 'failed', summary: '', ids: { run_id: 'run_9', step_id: 'create' } });
    expect(within(screen.getByText('create').closest('button')!).getByText('passed')).toBeInTheDocument();

    await act(async () => {
      releaseRun(finishedRun);
    });

    await waitFor(() => expect(screen.getByText('2/2 assertions')).toBeInTheDocument());
    // The run's own status, plus the step's -- the run panel and the step
    // card each show one.
    expect(screen.getAllByText('passed')).toHaveLength(2);
    // Still on the flow page.
    expect(screen.getByText('QCOM order')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Dismiss' }));
    expect(screen.queryByText('Open run details →')).not.toBeInTheDocument();
  });

  it('a local flow: shows its tier and promotes to the team with one click', async () => {
    const user = userEvent.setup();
    flowsGet.mockImplementationOnce(async () => flowAtTier('local'));
    renderPage();

    await waitFor(() => expect(screen.getByText('QCOM order')).toBeInTheDocument());
    // The badge sits with the title; the KeyValue's own "owner" row also says local.
    expect(screen.getByRole('heading', { name: 'QCOM order' }).parentElement).toHaveTextContent('local');
    expect(screen.queryByRole('button', { name: 'Move to local' })).not.toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: 'Promote to team' }));
    await waitFor(() => expect(flowsRescope).toHaveBeenCalledWith('qcom-order', 'workspace', undefined));
    // The page reloads to pick up the new tier.
    await waitFor(() => expect(flowsGet.mock.calls.length).toBeGreaterThanOrEqual(2));
  });

  it('promoting without ticking "and commit" leaves it uncommitted, and says so', async () => {
    const user = userEvent.setup();
    flowsGet.mockImplementationOnce(async () => flowAtTier('local'));
    renderPage();
    await waitFor(() => expect(screen.getByText('QCOM order')).toBeInTheDocument());

    const commitBox = screen.getByRole('checkbox', { name: /and commit in the workspace repo/i });
    expect(commitBox).not.toBeChecked();

    await user.click(screen.getByRole('button', { name: 'Promote to team' }));
    await waitFor(() => expect(flowsRescope).toHaveBeenCalledWith('qcom-order', 'workspace', undefined));
    await waitFor(() =>
      expect(useToasts.getState().toasts.some((t) => t.message === 'qcom-order moved to flows/, not committed yet.')).toBe(true),
    );
  });

  it('ticking "and commit" passes commit: true and reports it committed', async () => {
    const user = userEvent.setup();
    flowsGet.mockImplementationOnce(async () => flowAtTier('local'));
    renderPage();
    await waitFor(() => expect(screen.getByText('QCOM order')).toBeInTheDocument());

    await user.click(screen.getByRole('checkbox', { name: /and commit in the workspace repo/i }));
    await user.click(screen.getByRole('button', { name: 'Promote to team' }));

    await waitFor(() => expect(flowsRescope).toHaveBeenCalledWith('qcom-order', 'workspace', undefined, { commit: true }));
    await waitFor(() =>
      expect(useToasts.getState().toasts.some((t) => t.message === 'qcom-order moved to flows/ and committed.')).toBe(true),
    );
  });

  it('shows the Shipped badge in the header for a workspace-tier flow, from the flow\'s own summary', async () => {
    flowsList.mockImplementationOnce(
      async (): Promise<FlowSummary[]> => [
        {
          id: 'qcom-order',
          path: 'flows/qcom-order.flow.yaml',
          owner_kind: 'workspace',
          step_count: 1,
          hash: 'h',
          updated: '2026-01-01T00:00:00Z',
          shipped: 'unpushed',
        },
      ],
    );
    renderPage();
    await waitFor(() => expect(screen.getByText('QCOM order')).toBeInTheDocument());

    expect(await screen.findByText('committed, not pushed')).toBeInTheDocument();
    expect(flowsList).toHaveBeenCalledWith('qcom-order');
  });

  it('shows no Shipped badge when the summary lookup has no shipped value', async () => {
    renderPage();
    await waitFor(() => expect(screen.getByText('QCOM order')).toBeInTheDocument());

    for (const text of ['not committed', 'modified', 'committed, not pushed', 'shipped']) {
      expect(screen.queryByText(text)).not.toBeInTheDocument();
    }
  });

  it('shows a header Commit button for untracked/modified, and clicking it commits and refreshes the badge', async () => {
    const user = userEvent.setup();
    flowsList.mockImplementationOnce(
      async (): Promise<FlowSummary[]> => [
        {
          id: 'qcom-order',
          path: 'flows/qcom-order.flow.yaml',
          owner_kind: 'workspace',
          step_count: 1,
          hash: 'h',
          updated: '2026-01-01T00:00:00Z',
          shipped: 'untracked',
        },
      ],
    );
    renderPage();
    await waitFor(() => expect(screen.getByText('QCOM order')).toBeInTheDocument());
    const commitButton = await screen.findByRole('button', { name: 'Commit' });

    // Committing reloads the flow, whose next summary lookup reports "unpushed".
    flowsList.mockImplementationOnce(
      async (): Promise<FlowSummary[]> => [
        {
          id: 'qcom-order',
          path: 'flows/qcom-order.flow.yaml',
          owner_kind: 'workspace',
          step_count: 1,
          hash: 'h',
          updated: '2026-01-01T00:00:00Z',
          shipped: 'unpushed',
        },
      ],
    );
    await user.click(commitButton);

    await waitFor(() => expect(flowsCommit).toHaveBeenCalledWith('qcom-order', undefined));
    await waitFor(() => expect(screen.getByText('committed, not pushed')).toBeInTheDocument());
    expect(screen.queryByRole('button', { name: 'Commit' })).not.toBeInTheDocument();
    expect(useToasts.getState().toasts.some((t) => t.kind === 'success' && t.message === 'committed qcom-order; not pushed')).toBe(true);
  });

  it('shows no Commit button once a flow is already shipped or merely unpushed', async () => {
    flowsList.mockImplementationOnce(
      async (): Promise<FlowSummary[]> => [
        { id: 'qcom-order', path: 'flows/qcom-order.flow.yaml', owner_kind: 'workspace', step_count: 1, hash: 'h', updated: '2026-01-01T00:00:00Z', shipped: 'shipped' },
      ],
    );
    renderPage();
    await waitFor(() => expect(screen.getByText('QCOM order')).toBeInTheDocument());

    expect(await screen.findByText('shipped')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Commit' })).not.toBeInTheDocument();
  });

  it('shows an error toast when the commit fails, leaving the badge untouched', async () => {
    const user = userEvent.setup();
    flowsList.mockImplementationOnce(
      async (): Promise<FlowSummary[]> => [
        { id: 'qcom-order', path: 'flows/qcom-order.flow.yaml', owner_kind: 'workspace', step_count: 1, hash: 'h', updated: '2026-01-01T00:00:00Z', shipped: 'modified' },
      ],
    );
    flowsCommit.mockRejectedValueOnce(new Error('nothing to commit'));
    renderPage();
    await waitFor(() => expect(screen.getByText('QCOM order')).toBeInTheDocument());

    await user.click(await screen.findByRole('button', { name: 'Commit' }));

    await waitFor(() =>
      expect(useToasts.getState().toasts.some((t) => t.kind === 'error' && t.message === 'nothing to commit')).toBe(true),
    );
    expect(screen.getByText('modified')).toBeInTheDocument();
  });

  it('a team flow: offers local, and the service tier only for a called service bound to a checkout', async () => {
    const user = userEvent.setup();
    servicesList.mockImplementation(async () => [qcomBound, { ...qcomBound, id: 'billing', name: 'billing' }]);
    renderPage();

    await waitFor(() => expect(screen.getByText('QCOM order')).toBeInTheDocument());
    expect(screen.getByRole('heading', { name: 'QCOM order' }).parentElement).toHaveTextContent('team');
    expect(screen.getByRole('button', { name: 'Move to local' })).toBeInTheDocument();

    // Only qcom is called by the flow's steps; billing is bound but irrelevant.
    const select = await screen.findByRole('combobox', { name: 'Move to service' });
    expect(within(select).getByRole('option', { name: 'qcom' })).toBeInTheDocument();
    expect(within(select).queryByRole('option', { name: 'billing' })).not.toBeInTheDocument();

    await user.selectOptions(select, 'qcom');
    await waitFor(() => expect(flowsRescope).toHaveBeenCalledWith('qcom-order', 'service', 'qcom'));
  });

  it('a team flow whose services read from the team source cannot move to a service', async () => {
    renderPage();
    await waitFor(() => expect(screen.getByText('QCOM order')).toBeInTheDocument());
    await waitFor(() => expect(servicesList).toHaveBeenCalled());
    expect(screen.queryByRole('combobox', { name: 'Move to service' })).not.toBeInTheDocument();
    expect(screen.getByText(/none of qcom is read from a local checkout here/)).toBeInTheDocument();
  });

  it('a service flow: moves back to the team', async () => {
    const user = userEvent.setup();
    flowsGet.mockImplementationOnce(async () => flowAtTier('service', 'qcom'));
    renderPage();

    await waitFor(() => expect(screen.getByText('QCOM order')).toBeInTheDocument());
    expect(screen.getByRole('heading', { name: 'QCOM order' }).parentElement).toHaveTextContent('service:qcom');

    await user.click(screen.getByRole('button', { name: 'Move to team' }));
    await waitFor(() => expect(flowsRescope).toHaveBeenCalledWith('qcom-order', 'workspace', undefined));
  });

  it('warns once, then PUTs the edited YAML and clears the edits on "Save to flow"', async () => {
    const user = userEvent.setup();
    const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(true);
    renderPage();

    await waitFor(() => expect(screen.getByText('QCOM order')).toBeInTheDocument());
    fireEvent.click(screen.getByText('create').closest('button')!);
    const addRiderId = await screen.findByRole('button', { name: /riderId/ });
    await user.click(addRiderId);
    await user.type(screen.getByPlaceholderText('value'), 'rider_42');

    const saveButton = screen.getByRole('button', { name: 'Save to flow' });
    expect(saveButton).toBeEnabled();
    await user.click(saveButton);

    expect(confirmSpy).toHaveBeenCalledTimes(1);
    await waitFor(() => expect(flowsUpdate).toHaveBeenCalledTimes(1));
    const [flowId, req] = flowsUpdate.mock.calls[0] as [string, { yaml: string }];
    expect(flowId).toBe('qcom-order');
    expect(req.yaml).toContain('riderId: rider_42');

    // The edit is cleared once the save succeeds: "modified" disappears and
    // "Save to flow" goes back to disabled.
    await waitFor(() => expect(screen.queryByText('modified')).not.toBeInTheDocument());
    expect(screen.getByRole('button', { name: 'Save to flow' })).toBeDisabled();

    confirmSpy.mockRestore();
  });
});
