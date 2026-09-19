import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import UpdatesPanel from '../pages/settings/UpdatesPanel';
import { useDaemon } from '../state/daemon';
import type { UpdateInfo } from '../api/types';

const upToDate: UpdateInfo = {
  current: '1.3.1',
  latest: '1.3.1',
  available: false,
  checked_at: new Date(Date.now() - 60_000).toISOString(),
  can_self_upgrade: true,
  check_enabled: true,
};

const selfUpgradable: UpdateInfo = {
  ...upToDate,
  latest: '1.4.0',
  available: true,
  can_self_upgrade: true,
  release_url: 'https://github.com/example/sapien/releases/tag/v1.4.0',
};

const manualOnly: UpdateInfo = {
  ...selfUpgradable,
  can_self_upgrade: false,
  install_method: 'homebrew',
  command: 'brew upgrade sapien',
};

const get = vi.fn(async (): Promise<UpdateInfo> => upToDate);
const check = vi.fn(async (): Promise<UpdateInfo> => upToDate);
const apply = vi.fn(async (): Promise<void> => undefined);
const setCheckEnabled = vi.fn(async (_check: boolean): Promise<void> => undefined);

vi.mock('../api/client', () => ({
  updates: {
    get: () => get(),
    check: () => check(),
    apply: () => apply(),
    setCheckEnabled: (check: boolean) => setCheckEnabled(check),
  },
}));

const fetchMock = vi.fn();
function health(version: string) {
  fetchMock.mockResolvedValueOnce({ ok: true, json: async () => ({ ok: true, version, workspace: '/ws' }) });
}
function unreachable() {
  fetchMock.mockRejectedValueOnce(new TypeError('Failed to fetch'));
}

beforeEach(() => {
  get.mockReset().mockResolvedValue(upToDate);
  check.mockReset().mockResolvedValue(upToDate);
  apply.mockReset().mockResolvedValue(undefined);
  setCheckEnabled.mockReset().mockResolvedValue(undefined);
  fetchMock.mockReset();
  vi.stubGlobal('fetch', fetchMock);
  useDaemon.setState({ state: 'unknown', loadedVersion: null, runningVersion: null, sessionStale: false, restarting: false });
  Object.defineProperty(window, 'location', { configurable: true, value: { ...window.location, reload: vi.fn() } });
});

describe('UpdatesPanel', () => {
  it('renders current/latest/checked from GET, with no upgrade box when up to date', async () => {
    render(<UpdatesPanel />);
    await waitFor(() => expect(screen.getAllByText('1.3.1')).toHaveLength(2));
    expect(screen.getByText('1 minute ago')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /Upgrade to/ })).not.toBeInTheDocument();
  });

  it('Check now calls POST /v1/update/check and refetches', async () => {
    const user = userEvent.setup();
    render(<UpdatesPanel />);
    await waitFor(() => expect(screen.getByRole('button', { name: 'Check now' })).toBeInTheDocument());

    await user.click(screen.getByRole('button', { name: 'Check now' }));
    await waitFor(() => expect(check).toHaveBeenCalled());
    await waitFor(() => expect(get).toHaveBeenCalledTimes(2));
  });

  it('the daily-check toggle PUTs /v1/settings/updates', async () => {
    const user = userEvent.setup();
    render(<UpdatesPanel />);
    const toggle = await screen.findByRole('checkbox', { name: 'Check for updates daily' });
    expect(toggle).toBeChecked();

    await user.click(toggle);
    await waitFor(() => expect(setCheckEnabled).toHaveBeenCalledWith(false));
  });

  it('a self-upgradable build offers "Upgrade to vX", confirms, applies, waits for health, and reloads', async () => {
    get.mockResolvedValue(selfUpgradable);
    const user = userEvent.setup();
    render(<UpdatesPanel />);

    const upgradeButton = await screen.findByRole('button', { name: 'Upgrade to 1.4.0' });
    await user.click(upgradeButton);

    const dialog = screen.getByRole('dialog', { name: 'Upgrade to 1.4.0?' });
    unreachable();
    health('1.4.0');
    await user.click(within(dialog).getByRole('button', { name: 'Upgrade' }));

    await waitFor(() => expect(apply).toHaveBeenCalled());
    await waitFor(() => expect(window.location.reload).toHaveBeenCalled());
  });

  it('a non-self-upgradable build shows the manual command in a copyable box, naming the install method', async () => {
    get.mockResolvedValue(manualOnly);
    render(<UpdatesPanel />);

    await waitFor(() => expect(screen.getByText('brew upgrade sapien')).toBeInTheDocument());
    expect(screen.getByText(/Installed via homebrew/)).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /Upgrade to/ })).not.toBeInTheDocument();
  });

  it('links to release_url when available', async () => {
    get.mockResolvedValue(selfUpgradable);
    render(<UpdatesPanel />);
    await waitFor(() =>
      expect(screen.getByRole('link', { name: 'Release notes' })).toHaveAttribute(
        'href',
        'https://github.com/example/sapien/releases/tag/v1.4.0',
      ),
    );
  });
});
