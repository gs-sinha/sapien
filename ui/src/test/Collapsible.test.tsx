import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it } from 'vitest';
import { Collapsible } from '../components/Collapsible';

beforeEach(() => {
  localStorage.clear();
});

describe('Collapsible', () => {
  it('is collapsed by default, showing a header with the title, count, and aria-expanded', () => {
    render(
      <Collapsible storageKey="test.section" title="Warnings" count={3}>
        <p>body</p>
      </Collapsible>,
    );

    const toggle = screen.getByRole('button', { name: 'Warnings (3)' });
    expect(toggle).toHaveAttribute('aria-expanded', 'false');
    expect(screen.queryByText('body')).not.toBeInTheDocument();
  });

  it('honours defaultOpen when nothing is stored yet', () => {
    render(
      <Collapsible storageKey="test.open-by-default" title="Docs" count={1} defaultOpen>
        <p>body</p>
      </Collapsible>,
    );

    expect(screen.getByRole('button', { name: 'Docs (1)' })).toHaveAttribute('aria-expanded', 'true');
    expect(screen.getByText('body')).toBeInTheDocument();
  });

  it('toggles open on click and shows children', async () => {
    const user = userEvent.setup();
    render(
      <Collapsible storageKey="test.toggle" title="Warnings" count={2}>
        <p>the body text</p>
      </Collapsible>,
    );

    const toggle = screen.getByRole('button', { name: 'Warnings (2)' });
    await user.click(toggle);

    expect(toggle).toHaveAttribute('aria-expanded', 'true');
    expect(screen.getByText('the body text')).toBeInTheDocument();
  });

  it('shows a truncated summary line only while collapsed', async () => {
    const user = userEvent.setup();
    render(
      <Collapsible storageKey="test.summary" title="Docs" count={2} summary="doc-a, doc-b">
        <p>full docs here</p>
      </Collapsible>,
    );

    expect(screen.getByText('doc-a, doc-b')).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Docs (2)' }));
    expect(screen.queryByText('doc-a, doc-b')).not.toBeInTheDocument();
  });

  it('persists the open state in localStorage per storageKey, across remounts', async () => {
    const user = userEvent.setup();
    const { unmount } = render(
      <Collapsible storageKey="test.persist" title="Warnings" count={1}>
        <p>body</p>
      </Collapsible>,
    );
    await user.click(screen.getByRole('button', { name: 'Warnings (1)' }));
    expect(localStorage.getItem('sapien.collapsible.test.persist')).toBe('1');
    unmount();

    render(
      <Collapsible storageKey="test.persist" title="Warnings" count={1}>
        <p>body</p>
      </Collapsible>,
    );
    expect(screen.getByRole('button', { name: 'Warnings (1)' })).toHaveAttribute('aria-expanded', 'true');
    expect(screen.getByText('body')).toBeInTheDocument();
  });

  it('a different storageKey remembers its own state independently', async () => {
    const user = userEvent.setup();
    const { unmount } = render(
      <Collapsible storageKey="test.a" title="A" count={1}>
        <p>a-body</p>
      </Collapsible>,
    );
    await user.click(screen.getByRole('button', { name: 'A (1)' }));
    unmount();

    render(
      <Collapsible storageKey="test.b" title="B" count={1}>
        <p>b-body</p>
      </Collapsible>,
    );
    expect(screen.getByRole('button', { name: 'B (1)' })).toHaveAttribute('aria-expanded', 'false');
  });

  it('falls back to the default open state when localStorage throws', () => {
    const original = window.localStorage.getItem;
    // Simulate a private-mode/blocked-storage browser: reads must not crash the component.
    window.localStorage.getItem = () => {
      throw new Error('blocked');
    };
    try {
      render(
        <Collapsible storageKey="test.blocked" title="Warnings" count={1} defaultOpen>
          <p>body</p>
        </Collapsible>,
      );
      expect(screen.getByRole('button', { name: 'Warnings (1)' })).toHaveAttribute('aria-expanded', 'true');
    } finally {
      window.localStorage.getItem = original;
    }
  });
});
