import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import MemoryDetailPage from '../pages/MemoryDetailPage';
import type { Memory, PromotionTarget } from '../api/types';

const memoriesGet = vi.fn();
const memoriesPromotion = vi.fn();
const memoriesPatch = vi.fn();

vi.mock('../api/client', () => ({
  memories: {
    get: (...a: unknown[]) => memoriesGet(...a),
    promotion: (...a: unknown[]) => memoriesPromotion(...a),
    patch: (...a: unknown[]) => memoriesPatch(...a),
  },
}));

const memory: Memory = {
  id: 'mem_01H0000000000000000000',
  type: 'gotcha',
  scope: 'workspace',
  subject: { operation: 'orders.allocate', service: 'orders' },
  source: { kind: 'agent', client: 'claude-code' },
  status: 'active',
  created: '2026-01-01T00:00:00Z',
  updated: '2026-01-02T00:00:00Z',
  text: 'A 409 from orders.allocate means no rider was online, not a real conflict.',
  file_path: 'memories/orders.allocate.md',
};

const target: PromotionTarget = {
  kind: 'doc',
  file: 'services/orders/docs/allocation.md',
  line: 42,
  section: 'Error codes',
  current: 'Returns 409 when the request conflicts with another update.',
  memory,
  suggested: 'Returns 409 when no rider is currently online (not a real conflict).',
};

beforeEach(() => {
  memoriesGet.mockReset().mockResolvedValue(memory);
  memoriesPromotion.mockReset().mockResolvedValue(target);
  memoriesPatch.mockReset().mockResolvedValue(memory);
});

function renderPage() {
  return render(
    <MemoryRouter initialEntries={[`/ui/memories/${memory.id}`]}>
      <Routes>
        <Route path="/ui/memories/:id" element={<MemoryDetailPage />} />
      </Routes>
    </MemoryRouter>,
  );
}

describe('MemoryDetailPage', () => {
  it('renders the memory text and subject', async () => {
    renderPage();
    await waitFor(() => expect(screen.getByText(memory.id)).toBeInTheDocument());
    expect(screen.getByText(/no rider was online/)).toBeInTheDocument();
    expect(screen.getByText('orders.allocate')).toBeInTheDocument();
  });

  it('fetches and renders the promotion target on demand', async () => {
    const user = userEvent.setup();
    renderPage();
    await waitFor(() => expect(screen.getByText(memory.id)).toBeInTheDocument());

    expect(memoriesPromotion).not.toHaveBeenCalled();
    await user.click(screen.getByRole('button', { name: /where does this belong/i }));

    await waitFor(() => expect(memoriesPromotion).toHaveBeenCalledWith(memory.id));
    expect(await screen.findByText('services/orders/docs/allocation.md')).toBeInTheDocument();
    expect(screen.getByText(/:42/)).toBeInTheDocument();
    expect(screen.getByText('Error codes')).toBeInTheDocument();
    expect(screen.getByText(target.current!)).toBeInTheDocument();
    expect(screen.getByText(target.suggested!)).toBeInTheDocument();
  });
});
