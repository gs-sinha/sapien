import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes, useParams } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import FlowDetailPage from '../pages/FlowDetailPage';
import type { Operation, Run } from '../api/types';

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
    update: (id: string, req: { yaml: string }) => flowsUpdate(id, req),
    validate: vi.fn(async () => ({ valid: true, diagnostics: [] })),
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
    list: vi.fn(async () => [{ version: 1, name: 'stage', production: false }]),
    getDefault: vi.fn(async () => ({ name: 'stage' })),
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
});

afterEach(() => {
  flowsUpdate.mockClear();
  runsRunSource.mockClear();
});

describe('FlowDetailPage', () => {
  it('shows the source YAML and the materialized steps', async () => {
    const { container } = renderPage();

    await waitFor(() => expect(screen.getByText('QCOM order')).toBeInTheDocument());

    // Source YAML is rendered verbatim (line by line) rather than
    // reconstructed from the parsed fields. YamlView colours the "id" key
    // in its own <span>, so the line's full text is checked via the
    // container's textContent rather than getByText (which only concatenates
    // an element's direct text-node children, not text split across a
    // nested element -- see testing-library's own guidance on this).
    expect(container.textContent).toContain('id: qcom-order');
    expect(container.textContent).toContain('call: qcom.createOrder');

    // The materialized step shows up as an expandable card.
    expect(screen.getByText('create')).toBeInTheDocument();
    expect(screen.getByText('qcom.createOrder')).toBeInTheDocument();
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
    expect(req.opts).toEqual({ environment: 'stage', inputs: {}, trigger: 'ui' });

    // Landed on the new run's page.
    await waitFor(() => expect(screen.getByText('RUN PAGE run_1')).toBeInTheDocument());
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
