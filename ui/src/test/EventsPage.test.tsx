import { act, fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it } from 'vitest';
import EventsPage from '../pages/EventsPage';
import { useEvents } from '../state/events';
import type { StoredEvent } from '../state/events';

const sample: StoredEvent[] = [
  { type: 'run.step', time: '2026-01-01T00:00:00Z', summary: 'run r1 step call: passed', ids: { run_id: 'r1', step_id: 'call' } },
  { type: 'memory.created', time: '2026-01-01T00:01:00Z', summary: 'memory mem_1 created', ids: { memory_id: 'mem_1' } },
  { type: 'flow.changed', time: '2026-01-01T00:02:00Z', summary: 'flow f1', ids: { flow_id: 'f1' } },
];

beforeEach(() => {
  useEvents.setState({ events: sample, status: 'open', unreadCount: 0, started: false });
});

function renderPage() {
  return render(
    <MemoryRouter>
      <EventsPage />
    </MemoryRouter>,
  );
}

describe('EventsPage', () => {
  it('shows every event by default', () => {
    renderPage();
    expect(screen.getByText('run r1 step call: passed')).toBeInTheDocument();
    expect(screen.getByText('memory mem_1 created')).toBeInTheDocument();
    expect(screen.getByText('flow f1')).toBeInTheDocument();
  });

  it('filters down to just the selected type', () => {
    renderPage();
    fireEvent.click(screen.getByRole('button', { name: 'memory.created' }));

    expect(screen.getByText('memory mem_1 created')).toBeInTheDocument();
    expect(screen.queryByText('run r1 step call: passed')).not.toBeInTheDocument();
    expect(screen.queryByText('flow f1')).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'All' }));
    expect(screen.getByText('run r1 step call: passed')).toBeInTheDocument();
  });

  it('links event ids to their detail pages', () => {
    renderPage();
    expect(screen.getByRole('link', { name: 'run:r1' })).toHaveAttribute('href', '/ui/runs/r1');
    expect(screen.getByRole('link', { name: 'memory:mem_1' })).toHaveAttribute('href', '/ui/memories/mem_1');
    expect(screen.getByRole('link', { name: 'flow:f1' })).toHaveAttribute('href', '/ui/flows/f1');
  });

  it('buffers new events while paused, then flushes them on resume', () => {
    renderPage();
    fireEvent.click(screen.getByRole('button', { name: 'Pause' }));

    act(() => {
      useEvents.setState((s) => ({
        events: [...s.events, { type: 'flow.changed', time: '2026-01-01T00:03:00Z', summary: 'flow f2', ids: { flow_id: 'f2' } }],
      }));
    });

    expect(screen.queryByText('flow f2')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: /resume/i })).toHaveTextContent('1 buffered');

    fireEvent.click(screen.getByRole('button', { name: /resume/i }));
    expect(screen.getByText('flow f2')).toBeInTheDocument();
  });
});
