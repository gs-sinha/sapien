// Pending-friction-report count shown as a small badge next to the
// "Friction" nav entry (Nav.tsx). Fetched once when Nav mounts -- one
// `friction.list()` call, no polling -- and refreshed by FrictionPage
// itself whenever it sends or drops a report, so the badge and the page
// never drift without adding a poll or a new event type (friction reports
// have no event of their own; see internal/friction).
import { create } from 'zustand';
import { friction } from '../api/client';

interface FrictionCountState {
  pending: number;
  refresh: () => void;
}

export const useFrictionCount = create<FrictionCountState>((set) => ({
  pending: 0,
  refresh: () => {
    friction
      .list()
      .then((reports) => {
        set({ pending: reports.filter((r) => r.status === 'pending').length });
      })
      .catch(() => {
        // Daemon unreachable, or this build predates the route: leave the
        // last known count rather than flashing the badge to zero.
      });
  },
}));
