import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import TryIt from '../pages/TryIt';
import type { Environment, Operation, Run, SavedExample } from '../api/types';

const operationsGet = vi.fn();
const environmentsList = vi.fn();
const environmentsGetDefault = vi.fn();
const examplesList = vi.fn();
const examplesGet = vi.fn();
const callOperation = vi.fn();
const examplesFromRun = vi.fn();
const examplesCreate = vi.fn();

vi.mock('../api/client', () => ({
  operations: { get: (...a: unknown[]) => operationsGet(...a) },
  environments: {
    list: (...a: unknown[]) => environmentsList(...a),
    getDefault: (...a: unknown[]) => environmentsGetDefault(...a),
  },
  examples: {
    list: (...a: unknown[]) => examplesList(...a),
    get: (...a: unknown[]) => examplesGet(...a),
    fromRun: (...a: unknown[]) => examplesFromRun(...a),
    create: (...a: unknown[]) => examplesCreate(...a),
  },
  callOperation: (...a: unknown[]) => callOperation(...a),
}));

vi.mock('../api/tryExtra', () => ({
  getRunHints: vi.fn(async () => []),
}));

const op: Operation = {
  id: 'svc.getThing',
  service_id: 'svc',
  protocol: 'http',
  http: { method: 'GET', path: '/things/{id}' },
  source: { file: 'svc/openapi.yaml' },
  hash: 'h1',
  params: [{ name: 'id', in: 'path', required: true, schema: { kind: 'string' } }],
  request_body: {
    content_type: 'application/json',
    required: true,
    schema: { kind: 'object', required: ['note'], properties: { note: { kind: 'string' } }, property_order: ['note'] },
  },
};

const stageEnv: Environment = { version: 1, name: 'stage', production: false };
const prodEnv: Environment = { version: 1, name: 'prod', production: true };

const savedExample: SavedExample = {
  version: 1,
  id: 'ex1',
  operation: 'svc.getThing',
  scope: 'workspace',
  service: 'svc',
  input: { id: 'abc' },
  body: { foo: 'bar' },
  headers: { 'X-Test': '1' },
  created: '2026-01-01T00:00:00Z',
  updated: '2026-01-01T00:00:00Z',
};

function passedRun(overrides: Partial<Run> = {}): Run {
  return {
    id: 'run_1',
    environment: 'stage',
    status: 'passed',
    started: '2026-01-01T00:00:00Z',
    summary: { steps_total: 1, steps_passed: 1, steps_failed: 0, steps_errored: 0, steps_skipped: 0, assertions: 0, assertions_failed: 0 },
    steps: [
      {
        step_id: 'call',
        index: 0,
        status: 'passed',
        response: { status: 200, headers: { 'Content-Type': 'application/json' }, body: { ok: true }, size: 2 },
      },
    ],
    ...overrides,
  };
}

function renderTryIt(path = '/ui/try/svc.getThing') {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/ui/try/:operationId" element={<TryIt />} />
      </Routes>
    </MemoryRouter>,
  );
}

beforeEach(() => {
  operationsGet.mockReset().mockResolvedValue(op);
  environmentsList.mockReset().mockResolvedValue([stageEnv]);
  environmentsGetDefault.mockReset().mockResolvedValue({ name: 'stage' });
  examplesList.mockReset().mockResolvedValue([]);
  examplesGet.mockReset().mockResolvedValue(savedExample);
  callOperation.mockReset().mockResolvedValue(passedRun());
  examplesFromRun.mockReset().mockResolvedValue({ ...savedExample, id: 'saved-1' });
  examplesCreate.mockReset().mockResolvedValue({ ...savedExample, id: 'saved-2' });
  try {
    sessionStorage.clear();
  } catch {
    // ignore
  }
});

afterEach(() => {
  vi.clearAllMocks();
});

describe('TryIt', () => {
  it('prefills from ?example= and posts the matching call body', async () => {
    examplesList.mockResolvedValue([savedExample]);
    const user = userEvent.setup();
    renderTryIt('/ui/try/svc.getThing?example=ex1');

    const idInput = await screen.findByDisplayValue('abc');
    expect(idInput).toBeInTheDocument();

    // The placeholder's internal newlines get collapsed to a single space by
    // the default text normalizer, so the query matches on that form.
    const bodyBox = screen.getByPlaceholderText('{ }') as HTMLTextAreaElement;
    await waitFor(() => expect(bodyBox.value).toContain('"foo": "bar"'));

    await user.click(screen.getByRole('button', { name: 'Run' }));

    await waitFor(() => expect(callOperation).toHaveBeenCalledTimes(1));
    expect(callOperation).toHaveBeenCalledWith({
      operation: 'svc.getThing',
      params: { id: 'abc' },
      body: { foo: 'bar' },
      headers: { 'X-Test': '1' },
      env: 'stage',
      allow_production: false,
      trigger: 'ui',
    });

    await screen.findByText('200');
  });

  it('blocks Run against a production environment until allow-production is ticked', async () => {
    environmentsList.mockResolvedValue([prodEnv]);
    environmentsGetDefault.mockResolvedValue({ name: 'prod' });
    const user = userEvent.setup();
    renderTryIt();

    await screen.findByText('This environment is marked production.');
    const runButton = screen.getByRole('button', { name: 'Run' });
    expect(runButton).toBeDisabled();

    await user.click(screen.getByRole('checkbox', { name: /allow running against production/i }));
    expect(runButton).toBeEnabled();

    await user.click(runButton);
    await waitFor(() => expect(callOperation).toHaveBeenCalledTimes(1));
    expect(callOperation.mock.calls[0][0]).toMatchObject({ env: 'prod', allow_production: true });
  });

  it('saves a run as an example via POST /v1/examples/from-run', async () => {
    const user = userEvent.setup();
    renderTryIt();

    await screen.findByText(op.id, { exact: false });
    await user.click(screen.getByRole('button', { name: 'Run' }));
    await screen.findByText('Save as example');
    await user.click(screen.getByRole('button', { name: 'Save as example' }));

    const dialog = await screen.findByRole('dialog', { name: 'Save as example' });
    const idField = within(dialog).getByLabelText(/example id/i);
    await user.clear(idField);
    await user.type(idField, 'my-example');
    await user.click(within(dialog).getByRole('button', { name: 'Save' }));

    await waitFor(() => expect(examplesFromRun).toHaveBeenCalledTimes(1));
    expect(examplesFromRun).toHaveBeenCalledWith({
      run_id: 'run_1',
      step_id: 'call',
      id: 'my-example',
      description: undefined,
      scope: 'workspace',
      tags: undefined,
      source: { kind: 'user' },
    });
  });
});
