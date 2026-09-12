import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { EditAndRerunPanel } from '../components/run/EditAndRerunPanel';
import type { Environment, Run } from '../api/types';

const environmentsList = vi.fn(async (): Promise<Environment[]> => [stageEnv, prodEnv]);
const environmentsGetDefault = vi.fn(async () => ({ name: 'stage' }));
const runsRunSource = vi.fn(async (_req: { yaml: string; opts: unknown }): Promise<Run> => rerun);

vi.mock('../api/client', () => ({
  flows: { get: vi.fn(), update: vi.fn(), create: vi.fn() },
  runs: { runSource: (req: { yaml: string; opts: unknown }) => runsRunSource(req) },
  environments: {
    list: () => environmentsList(),
    getDefault: () => environmentsGetDefault(),
  },
  ApiClientError: class extends Error {},
}));

const stageEnv: Environment = { version: 1, name: 'stage', production: false };
const prodEnv: Environment = { version: 1, name: 'prod', production: true };

const summary = { steps_total: 1, steps_passed: 1, steps_failed: 0, steps_errored: 0, steps_skipped: 0, assertions: 0, assertions_failed: 0 };

// A flowless (ad hoc) run, so the panel seeds its editor from
// flow_snapshot and never has to wait on GET /v1/flows/{id}.
function adHocRun(environment: string): Run {
  return {
    id: 'run_1',
    environment,
    status: 'passed',
    started: '2026-01-01T00:00:00Z',
    flow_snapshot: 'version: 1\nsteps:\n  - id: create\n    call: qcom.createOrder\n',
    summary,
  };
}

const rerun: Run = { id: 'run_2', environment: 'prod', status: 'passed', started: '2026-01-01T00:01:00Z', summary };

function renderPanel(run: Run) {
  return render(
    <MemoryRouter>
      <EditAndRerunPanel run={run} onClose={() => {}} />
    </MemoryRouter>,
  );
}

afterEach(() => {
  runsRunSource.mockClear();
});

describe('EditAndRerunPanel', () => {
  it('reruns a non-production run without unlocking anything', async () => {
    const user = userEvent.setup();
    renderPanel(adHocRun('stage'));

    const runButton = await screen.findByRole('button', { name: 'Run' });
    await waitFor(() => expect(runButton).toBeEnabled());
    expect(screen.queryByText('This environment is marked production.')).not.toBeInTheDocument();

    await user.click(runButton);
    await waitFor(() => expect(runsRunSource).toHaveBeenCalledTimes(1));
    expect(runsRunSource.mock.calls[0][0].opts).toMatchObject({ environment: 'stage', allow_production: false });
  });

  // Rerunning a run that was itself against production starts blocked: the
  // panel seeds the environment from the run, so the tick is the only way
  // through even though the user never touched the dropdown.
  it('blocks rerunning a production run until allow-production is ticked', async () => {
    const user = userEvent.setup();
    renderPanel(adHocRun('prod'));

    await screen.findByText('This environment is marked production.');
    const runButton = screen.getByRole('button', { name: 'Run' });
    expect(runButton).toBeDisabled();

    await user.click(screen.getByRole('checkbox', { name: /allow running against production/i }));
    expect(runButton).toBeEnabled();

    await user.click(runButton);
    await waitFor(() => expect(runsRunSource).toHaveBeenCalledTimes(1));
    expect(runsRunSource.mock.calls[0][0].opts).toMatchObject({ environment: 'prod', allow_production: true });
  });
});
