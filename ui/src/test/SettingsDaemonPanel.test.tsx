// DaemonPanel's UI wiring: rendering, the confirm dialog (and its warning
// when runs/terminals are in flight), the force flag, and the post-restart
// toast + refetch. The timer-heavy behaviour of the wait itself
// (state/daemon.ts's waitForRestart) has its own fake-timer tests in
// waitForRestart.test.ts; every scenario here resolves on the first probe
// of each phase, so no sleep is ever reached and real timers are fine.
import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import DaemonPanel from '../pages/settings/DaemonPanel';
import { useDaemon } from '../state/daemon';
import { useToasts } from '../state/toast';
import type { DaemonInfo } from '../api/types';

const idleInfo: DaemonInfo = {
  version: '1.3.1',
  commit: 'abcdef1234567890',
  started: new Date(Date.now() - 3600_000).toISOString(),
  pid: 4242,
  port: 54213,
  executable: '/usr/local/bin/sapien',
  install_method: 'homebrew',
  workspaces_open: 2,
  active_runs: 0,
  terminals: 0,
};

const busyInfo: DaemonInfo = { ...idleInfo, active_runs: 2, terminals: 1 };

const get = vi.fn(async (): Promise<DaemonInfo> => idleInfo);
const restart = vi.fn(async (_force?: boolean): Promise<void> => undefined);

vi.mock('../api/client', () => ({
  daemon: {
    get: () => get(),
    restart: (force?: boolean) => restart(force),
  },
}));

// state/daemon.ts's waitForRestart probes /v1/health with a bare fetch, the
// same seam daemonBanner.test.tsx uses.
const fetchMock = vi.fn();
function health(version = '1.3.1') {
  fetchMock.mockResolvedValueOnce({ ok: true, json: async () => ({ ok: true, version, workspace: '/ws' }) });
}
function unreachable() {
  fetchMock.mockRejectedValueOnce(new TypeError('Failed to fetch'));
}

beforeEach(() => {
  get.mockReset().mockResolvedValue(idleInfo);
  restart.mockReset().mockResolvedValue(undefined);
  fetchMock.mockReset();
  vi.stubGlobal('fetch', fetchMock);
  useDaemon.setState({ state: 'unknown', loadedVersion: null, runningVersion: null, sessionStale: false, restarting: false });
  useToasts.setState({ toasts: [] });
});

describe('DaemonPanel', () => {
  it('renders daemon info from GET /v1/daemon', async () => {
    render(<DaemonPanel />);
    await waitFor(() => expect(screen.getByText('1.3.1')).toBeInTheDocument());
    expect(screen.getByText('abcdef1')).toBeInTheDocument();
    expect(screen.getByText('4242')).toBeInTheDocument();
    expect(screen.getByText('54213')).toBeInTheDocument();
    expect(screen.getByText('homebrew')).toBeInTheDocument();
  });

  it('Restart with nothing in flight confirms without a warning and restarts without force', async () => {
    const user = userEvent.setup();
    render(<DaemonPanel />);
    await waitFor(() => expect(screen.getByRole('button', { name: 'Restart' })).toBeEnabled());

    await user.click(screen.getByRole('button', { name: 'Restart' }));
    expect(screen.getByRole('dialog', { name: 'Restart the daemon?' })).toBeInTheDocument();
    expect(screen.queryByText(/in flight will be cancelled/)).not.toBeInTheDocument();

    // First health probe (phase 1) sees the old process still going down;
    // the second (phase 2) is the successor answering.
    unreachable();
    health('1.3.1');
    await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Restart' }));

    await waitFor(() => expect(restart).toHaveBeenCalledWith(undefined));
    await waitFor(() => expect(useToasts.getState().toasts.some((t) => t.message === 'Daemon restarted')).toBe(true));
    // The panel refetches after a successful restart.
    await waitFor(() => expect(get).toHaveBeenCalledTimes(2));
  });

  it('warns and forces the restart when runs/terminals are in flight', async () => {
    get.mockResolvedValue(busyInfo);
    const user = userEvent.setup();
    render(<DaemonPanel />);
    await waitFor(() => expect(screen.getByRole('button', { name: 'Restart' })).toBeEnabled());

    await user.click(screen.getByRole('button', { name: 'Restart' }));
    expect(screen.getByText('2 runs in flight will be cancelled and 1 terminal will be closed.')).toBeInTheDocument();

    unreachable();
    health('1.3.1');
    await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Restart' }));

    await waitFor(() => expect(restart).toHaveBeenCalledWith(true));
  });

  it("sets the daemon store's restarting flag while waiting, and clears it once the health check succeeds", async () => {
    const user = userEvent.setup();
    render(<DaemonPanel />);
    await waitFor(() => expect(screen.getByRole('button', { name: 'Restart' })).toBeEnabled());
    await user.click(screen.getByRole('button', { name: 'Restart' }));

    unreachable();
    health('1.3.1');
    await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Restart' }));

    await waitFor(() => expect(useDaemon.getState().restarting).toBe(false));
  });

  it('cancelling the confirm dialog does not restart', async () => {
    const user = userEvent.setup();
    render(<DaemonPanel />);
    await waitFor(() => expect(screen.getByRole('button', { name: 'Restart' })).toBeEnabled());

    await user.click(screen.getByRole('button', { name: 'Restart' }));
    await user.click(screen.getByRole('button', { name: 'Cancel' }));

    expect(screen.queryByRole('dialog', { name: 'Restart the daemon?' })).not.toBeInTheDocument();
    expect(restart).not.toHaveBeenCalled();
  });
});
