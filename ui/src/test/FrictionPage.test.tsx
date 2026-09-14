import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import FrictionPage from '../pages/FrictionPage';
import type { FrictionPreview, FrictionReport } from '../api/types';

const frictionList = vi.fn();
const frictionPreview = vi.fn();
const frictionSend = vi.fn();
const frictionDrop = vi.fn();

vi.mock('../api/client', () => ({
  friction: {
    list: (...a: unknown[]) => frictionList(...a),
    preview: (...a: unknown[]) => frictionPreview(...a),
    send: (...a: unknown[]) => frictionSend(...a),
    drop: (...a: unknown[]) => frictionDrop(...a),
  },
}));

// state/friction.ts's refresh() calls friction.list() through the same
// mocked module above; nothing else to stub.

const pendingReport: FrictionReport = {
  id: 'fr_pending1',
  title: 'get_api returned the wrong shape',
  category: 'bug',
  tool: 'get_api',
  tried: 'Fetch the schema for orders.createOrder',
  happened: 'The response body did not match the documented Operation shape.',
  would_help: 'A schema example in the docs.',
  workspace: 'demo',
  client: 'claude-code',
  version: '1.2.0',
  created: '2026-01-01T00:00:00Z',
  status: 'pending',
};

const sentReport: FrictionReport = {
  id: 'fr_sent1',
  title: 'Docs missing a retry example',
  category: 'docs',
  happened: 'Could not find how to retry a failed step.',
  created: '2026-01-02T00:00:00Z',
  status: 'sent',
  sent_url: 'https://github.com/acme/sapien/discussions/42',
  sent_at: '2026-01-03T00:00:00Z',
};

const preview: FrictionPreview = {
  title: 'get_api returned the wrong shape',
  body: '## What happened\n\nThe response body did not match the documented Operation shape.',
  repo: 'acme/sapien',
  category: 'Ideas',
};

function renderPage() {
  return render(
    <MemoryRouter>
      <FrictionPage />
    </MemoryRouter>,
  );
}

beforeEach(() => {
  frictionList.mockReset().mockResolvedValue([pendingReport, sentReport]);
  frictionPreview.mockReset().mockResolvedValue(preview);
  frictionSend.mockReset().mockResolvedValue({ ...pendingReport, status: 'sent', sent_url: 'https://github.com/acme/sapien/discussions/99' });
  frictionDrop.mockReset().mockResolvedValue(undefined);
});

describe('FrictionPage', () => {
  it('lists reports with category, filed time, and status', async () => {
    renderPage();

    await waitFor(() => expect(screen.getByText('get_api returned the wrong shape')).toBeInTheDocument());
    expect(screen.getByText('Docs missing a retry example')).toBeInTheDocument();
    expect(screen.getByText('bug')).toBeInTheDocument();
    expect(screen.getByText('docs')).toBeInTheDocument();
    expect(screen.getByText('pending')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'sent' })).toHaveAttribute('href', sentReport.sent_url);
  });

  it('expands a row to show tried/happened/would_help and metadata', async () => {
    renderPage();
    await waitFor(() => expect(screen.getByText('get_api returned the wrong shape')).toBeInTheDocument());

    fireEvent.click(screen.getByText('get_api returned the wrong shape'));

    expect(screen.getByText(pendingReport.tried!)).toBeInTheDocument();
    expect(screen.getByText(pendingReport.happened)).toBeInTheDocument();
    expect(screen.getByText(pendingReport.would_help!)).toBeInTheDocument();
    expect(screen.getByText('get_api')).toBeInTheDocument();
    expect(screen.getByText('claude-code')).toBeInTheDocument();
    expect(screen.getByText('demo')).toBeInTheDocument();
    expect(screen.getByText('1.2.0')).toBeInTheDocument();
  });

  it('disables Post as Discussion until Preview has been shown, then sends and shows the URL', async () => {
    renderPage();
    await waitFor(() => expect(screen.getByText('get_api returned the wrong shape')).toBeInTheDocument());
    fireEvent.click(screen.getByText('get_api returned the wrong shape'));

    const postButton = screen.getByRole('button', { name: 'Post as Discussion' });
    expect(postButton).toBeDisabled();

    fireEvent.click(screen.getByRole('button', { name: 'Preview' }));
    await waitFor(() => expect(frictionPreview).toHaveBeenCalledWith('fr_pending1'));
    // The Markdown body has embedded blank lines, which RTL's default text
    // normalizer collapses -- compare the <pre> block's raw textContent
    // instead of going through getByText's normalized matching.
    await waitFor(() => expect(document.querySelector('pre')?.textContent).toBe(preview.body));
    expect(screen.getByText(/acme\/sapien/)).toBeInTheDocument();

    expect(postButton).not.toBeDisabled();

    // Not `...Once`: sending triggers both the page's own reload() and the
    // nav badge store's refresh(), which race to call friction.list() --
    // both must see the same post-send list.
    frictionList.mockResolvedValue([
      { ...pendingReport, status: 'sent', sent_url: 'https://github.com/acme/sapien/discussions/99' },
      sentReport,
    ]);
    fireEvent.click(postButton);

    await waitFor(() => expect(frictionSend).toHaveBeenCalledWith('fr_pending1'));
    await waitFor(() => {
      // The expanded preview block repeats the report's title in a <span>;
      // find the title <td> specifically to land on the data row, not the
      // preview row underneath it.
      const titleCell = screen.getAllByText('get_api returned the wrong shape').find((el) => el.tagName === 'TD')!;
      const row = titleCell.closest('tr')!;
      expect(within(row).getByRole('link', { name: 'sent' })).toHaveAttribute('href', 'https://github.com/acme/sapien/discussions/99');
    });
  });

  it('drops a report', async () => {
    renderPage();
    await waitFor(() => expect(screen.getByText('get_api returned the wrong shape')).toBeInTheDocument());
    fireEvent.click(screen.getByText('get_api returned the wrong shape'));

    frictionList.mockResolvedValue([sentReport]);
    const row = screen.getByText('get_api returned the wrong shape').closest('tr')!;
    fireEvent.click(within(row).getByRole('button', { name: 'Drop' }));

    await waitFor(() => expect(frictionDrop).toHaveBeenCalledWith('fr_pending1'));
    await waitFor(() => expect(screen.queryByText('get_api returned the wrong shape')).not.toBeInTheDocument());
  });

  it('filters out sent reports when the Sent chip is toggled off', async () => {
    renderPage();
    await waitFor(() => expect(screen.getByText('Docs missing a retry example')).toBeInTheDocument());

    fireEvent.click(screen.getByRole('button', { name: 'Sent' }));

    expect(screen.queryByText('Docs missing a retry example')).not.toBeInTheDocument();
    expect(screen.getByText('get_api returned the wrong shape')).toBeInTheDocument();
  });

  it('shows an empty state when there are no reports', async () => {
    frictionList.mockReset().mockResolvedValue([]);
    renderPage();

    await waitFor(() => expect(screen.getByText('No friction reports')).toBeInTheDocument());
    expect(screen.getByText(/report_friction MCP tool/)).toBeInTheDocument();
  });
});
