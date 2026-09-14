import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, useLocation } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { Nav } from '../components/Nav';
import { SearchBox } from '../components/SearchBox';
import { useFrictionCount } from '../state/friction';
import type { FrictionReport } from '../api/types';

const frictionList = vi.fn();

// Only `friction` is overridden -- WorkspacePicker (rendered by Nav) calls
// the real `workspacesApi`, whose own client-level .catch() already
// tolerates a daemon that isn't there in this test.
vi.mock('../api/client', async () => {
  const actual = await vi.importActual<typeof import('../api/client')>('../api/client');
  return { ...actual, friction: { list: (...a: unknown[]) => frictionList(...a) } };
});

function LocationProbe({ onChange }: { onChange: (path: string) => void }) {
  const loc = useLocation();
  onChange(loc.pathname + loc.search);
  return null;
}

beforeEach(() => {
  frictionList.mockReset().mockResolvedValue([]);
  useFrictionCount.setState({ pending: 0 });
});

describe('layout shell', () => {
  it('renders every nav link', () => {
    render(
      <MemoryRouter initialEntries={['/ui/flows']}>
        <Nav />
      </MemoryRouter>,
    );
    for (const label of ['Flows', 'Runs', 'Services', 'Operations', 'Examples', 'Memories', 'Friction', 'Events']) {
      expect(screen.getByRole('link', { name: label })).toBeInTheDocument();
    }
  });

  it('badges the Friction link with the pending-report count', async () => {
    const reports: FrictionReport[] = [
      { id: 'fr_1', title: 'a', category: 'bug', happened: 'x', created: '2026-01-01T00:00:00Z', status: 'pending' },
      { id: 'fr_2', title: 'b', category: 'idea', happened: 'y', created: '2026-01-01T00:00:00Z', status: 'sent' },
      { id: 'fr_3', title: 'c', category: 'docs', happened: 'z', created: '2026-01-01T00:00:00Z', status: 'pending' },
    ];
    frictionList.mockResolvedValue(reports);

    render(
      <MemoryRouter initialEntries={['/ui/flows']}>
        <Nav />
      </MemoryRouter>,
    );

    await waitFor(() => expect(screen.getByRole('link', { name: 'Friction 2' })).toBeInTheDocument());
  });

  it('search box navigates to ranked operation search', async () => {
    const user = userEvent.setup();
    let current = '';
    render(
      <MemoryRouter initialEntries={['/ui/flows']}>
        <SearchBox />
        <LocationProbe onChange={(p) => (current = p)} />
      </MemoryRouter>,
    );

    const input = screen.getByPlaceholderText(/search intent/i);
    await user.type(input, 'allocate rider{Enter}');

    expect(current).toBe('/ui/operations?q=allocate%20rider');
  });
});
