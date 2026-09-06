import { render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { DocViewer } from '../components/docs/DocViewer';
import { docs } from '../api/client';
import type { Doc } from '../api/types';

vi.mock('../api/client', () => ({
  docs: {
    get: vi.fn(),
  },
  findDocSection: vi.fn(() => undefined),
}));

describe('DocViewer', () => {
  afterEach(() => {
    vi.mocked(docs.get).mockReset();
  });

  it('renders the doc sections on a successful fetch', async () => {
    const doc: Doc = {
      id: 'orders/docs/allocation.md',
      service_id: 'orders',
      path: 'docs/allocation.md',
      title: 'Allocation',
      source: 'file',
      hash: 'h1',
      sections: [{ id: 'orders/docs/allocation.md#intro', ord: 0, heading: 'Intro', level: 1, body: 'How allocation works.' }],
    };
    vi.mocked(docs.get).mockResolvedValue(doc);

    render(
      <MemoryRouter>
        <DocViewer service="orders" path="docs/allocation.md" operations={[]} onClose={() => {}} />
      </MemoryRouter>,
    );

    await waitFor(() => expect(screen.getByRole('heading', { name: 'Allocation' })).toBeInTheDocument());
    expect(screen.getByText('Intro')).toBeInTheDocument();
    expect(screen.getByText('How allocation works.')).toBeInTheDocument();
    expect(screen.queryByText(/unavailable/i)).not.toBeInTheDocument();
  });

  it('shows a muted note instead of an error when the full-text fetch 404s', async () => {
    vi.mocked(docs.get).mockRejectedValue(new Error('not found'));

    render(
      <MemoryRouter>
        <DocViewer service="orders" path="contract#tag:Awb" operations={[]} onClose={() => {}} />
      </MemoryRouter>,
    );

    await waitFor(() => expect(screen.getByText('Full text unavailable for this doc.')).toBeInTheDocument());
    // No thrown error reaches the page -- the drawer still renders its
    // header/close affordance instead of an error boundary taking over.
    expect(screen.getByRole('button', { name: 'Close' })).toBeInTheDocument();
  });
});
