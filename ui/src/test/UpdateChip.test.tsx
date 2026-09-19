import { render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { UpdateChip } from '../components/UpdateChip';
import type { UpdateInfo } from '../api/types';

const get = vi.fn(async (): Promise<UpdateInfo> => ({ current: '1.3.1', available: false, check_enabled: true, can_self_upgrade: true }));

vi.mock('../api/client', () => ({
  updates: { get: () => get() },
}));

function renderChip() {
  return render(
    <MemoryRouter>
      <UpdateChip />
    </MemoryRouter>,
  );
}

beforeEach(() => {
  get.mockReset();
});

describe('UpdateChip', () => {
  it('renders nothing while nothing is available', async () => {
    get.mockResolvedValue({ current: '1.3.1', available: false, check_enabled: true, can_self_upgrade: true });
    const { container } = renderChip();
    await waitFor(() => expect(get).toHaveBeenCalled());
    expect(container).toBeEmptyDOMElement();
  });

  it('shows "vX available" linking to /ui/settings#updates when one is', async () => {
    get.mockResolvedValue({ current: '1.3.1', latest: '1.4.0', available: true, check_enabled: true, can_self_upgrade: true });
    renderChip();

    const link = await screen.findByRole('link', { name: 'v1.4.0 available' });
    expect(link).toHaveAttribute('href', '/ui/settings#updates');
  });

  it('fails silently (renders nothing) when the GET rejects, e.g. an old daemon with no route', async () => {
    get.mockRejectedValue(new Error('E_HTTP_404'));
    const { container } = renderChip();
    await waitFor(() => expect(get).toHaveBeenCalled());
    expect(container).toBeEmptyDOMElement();
  });
});
