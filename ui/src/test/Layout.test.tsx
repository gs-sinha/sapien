import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, useLocation } from 'react-router-dom';
import { describe, expect, it } from 'vitest';
import { Nav } from '../components/Nav';
import { SearchBox } from '../components/SearchBox';

function LocationProbe({ onChange }: { onChange: (path: string) => void }) {
  const loc = useLocation();
  onChange(loc.pathname + loc.search);
  return null;
}

describe('layout shell', () => {
  it('renders every nav link', () => {
    render(
      <MemoryRouter initialEntries={['/ui/flows']}>
        <Nav />
      </MemoryRouter>,
    );
    for (const label of ['Flows', 'Runs', 'Services', 'Operations', 'Examples', 'Memories', 'Events']) {
      expect(screen.getByRole('link', { name: label })).toBeInTheDocument();
    }
  });

  it('search box navigates to /ui/operations?intent=<text>', async () => {
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

    expect(current).toBe('/ui/operations?intent=allocate%20rider');
  });
});
