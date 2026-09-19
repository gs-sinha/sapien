import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { DaemonBanner } from '../components/DaemonBanner';
import { setSessionStale, useDaemon } from '../state/daemon';

// probeDaemon is a bare fetch against /v1/health by design (it has to work
// with a stale cookie and no workspace header), so the honest seam is
// fetch itself rather than a module mock.
const fetchMock = vi.fn();

function answers(version: string) {
  fetchMock.mockResolvedValue({ ok: true, json: async () => ({ ok: true, version, workspace: '/ws' }) });
}
function nothingListening() {
  fetchMock.mockRejectedValue(new TypeError('Failed to fetch'));
}

function reset() {
  useDaemon.setState({ state: 'unknown', loadedVersion: null, runningVersion: null, sessionStale: false, restarting: false });
}

beforeEach(() => {
  reset();
  fetchMock.mockReset();
  vi.stubGlobal('fetch', fetchMock);
});

describe('daemon staleness detection', () => {
  it('takes the first answer as the build this tab was served by', async () => {
    answers('1.1.0');
    await useDaemon.getState().probe();

    expect(useDaemon.getState().state).toBe('ok');
    expect(useDaemon.getState().loadedVersion).toBe('1.1.0');
  });

  it('reports a replacement when the answering build changes', async () => {
    answers('1.1.0');
    await useDaemon.getState().probe();
    answers('1.2.0');
    await useDaemon.getState().probe();

    expect(useDaemon.getState().state).toBe('replaced');
    expect(useDaemon.getState().runningVersion).toBe('1.2.0');
  });

  it('reports the same build coming back as fine, not as a replacement', async () => {
    // An idle-exited daemon relaunched at the same version: the socket's
    // own retry handles it and the user needs to be told nothing.
    answers('1.1.0');
    await useDaemon.getState().probe();
    nothingListening();
    await useDaemon.getState().probe();
    expect(useDaemon.getState().state).toBe('gone');

    answers('1.1.0');
    await useDaemon.getState().probe();
    expect(useDaemon.getState().state).toBe('ok');
  });

  it('reports nothing listening as gone', async () => {
    answers('1.1.0');
    await useDaemon.getState().probe();
    nothingListening();
    await useDaemon.getState().probe();

    expect(useDaemon.getState().state).toBe('gone');
  });
});

describe('DaemonBanner', () => {
  it('renders nothing while the daemon is the one that served this tab', () => {
    useDaemon.setState({ state: 'ok', loadedVersion: '1.1.0' });
    const { container } = render(<DaemonBanner />);
    expect(container).toBeEmptyDOMElement();
  });

  it('names both versions and offers a reload after an upgrade', async () => {
    const reload = vi.fn();
    Object.defineProperty(window, 'location', {
      configurable: true,
      value: { ...window.location, reload },
    });
    useDaemon.setState({ state: 'replaced', loadedVersion: '1.1.0', runningVersion: '1.2.0' });

    render(<DaemonBanner />);
    expect(screen.getByRole('status')).toHaveTextContent('1.1.0 → 1.2.0');

    await userEvent.click(screen.getByRole('button', { name: 'Reload' }));
    expect(reload).toHaveBeenCalled();
  });

  // PLAN §34f items 3/4: a Settings-initiated restart/upgrade briefly makes
  // a health probe see 'gone' while the old process exits, which must not
  // flash the "wasn't running" banner over a restart the user just asked
  // for -- state/daemon.ts's waitForRestart sets `restarting` for exactly
  // this window.
  it('suppresses the gone banner while an intentional restart is in progress', () => {
    useDaemon.setState({ state: 'gone', loadedVersion: '1.1.0', restarting: true });
    const { container } = render(<DaemonBanner />);
    expect(container).toBeEmptyDOMElement();
  });

  it('shows the gone banner again once restarting clears without the daemon coming back', () => {
    nothingListening();
    useDaemon.setState({ state: 'gone', loadedVersion: '1.1.0', restarting: false });
    render(<DaemonBanner />);
    expect(screen.getByRole('status')).toHaveTextContent('30 minutes');
  });

  it('explains the idle exit and re-probes on demand when nothing is listening', async () => {
    nothingListening();
    useDaemon.setState({ state: 'gone', loadedVersion: '1.1.0' });

    render(<DaemonBanner />);
    expect(screen.getByRole('status')).toHaveTextContent('30 minutes');
    expect(screen.getByRole('status')).toHaveTextContent('sapien ui');

    await userEvent.click(screen.getByRole('button', { name: 'Check again' }));
    expect(fetchMock).toHaveBeenCalledWith('/v1/health', expect.anything());
  });
});

describe('stale session', () => {
  it('is reported when the daemon is reachable but refuses this tab', () => {
    // The failure this catches: /v1/health is unauthenticated, so the
    // daemon looks fine, while every real request 401s against a token
    // minted by a daemon that has since restarted.
    setSessionStale(true);
    useDaemon.setState({ state: 'ok', loadedVersion: '1.1.0' });

    render(<DaemonBanner />);
    expect(screen.getByRole('status')).toHaveTextContent('previous daemon');
    expect(screen.getByRole('status')).toHaveTextContent('sapien ui');
  });

  it('clears on the first request that succeeds', () => {
    setSessionStale(true);
    setSessionStale(false);
    useDaemon.setState({ state: 'ok', loadedVersion: '1.1.0' });

    const { container } = render(<DaemonBanner />);
    expect(container).toBeEmptyDOMElement();
  });

  it('defers to a daemon that is gone, which explains the refusal anyway', () => {
    setSessionStale(true);
    useDaemon.setState({ state: 'gone', loadedVersion: '1.1.0' });

    render(<DaemonBanner />);
    expect(screen.getByRole('status')).toHaveTextContent('isn’t running');
  });
});
