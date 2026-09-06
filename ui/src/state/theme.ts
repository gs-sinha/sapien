// Theme store: follows prefers-color-scheme until the user explicitly
// toggles, after which the explicit choice persists in localStorage and
// wins over the system setting.
import { create } from 'zustand';

export type Theme = 'light' | 'dark';

const storageKey = 'sapien-theme';

function systemTheme(): Theme {
  return window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
}

function storedTheme(): Theme | null {
  try {
    const v = localStorage.getItem(storageKey);
    return v === 'dark' || v === 'light' ? v : null;
  } catch {
    return null;
  }
}

function applyTheme(theme: Theme) {
  document.documentElement.classList.toggle('dark', theme === 'dark');
}

interface ThemeState {
  theme: Theme;
  explicit: boolean;
  toggle: () => void;
  init: () => void;
}

export const useTheme = create<ThemeState>((set, get) => ({
  theme: 'light',
  explicit: false,

  init: () => {
    const stored = storedTheme();
    const theme = stored ?? systemTheme();
    applyTheme(theme);
    set({ theme, explicit: stored !== null });

    if (!stored && window.matchMedia) {
      const mql = window.matchMedia('(prefers-color-scheme: dark)');
      const onChange = (e: MediaQueryListEvent) => {
        if (get().explicit) return;
        const next: Theme = e.matches ? 'dark' : 'light';
        applyTheme(next);
        set({ theme: next });
      };
      mql.addEventListener?.('change', onChange);
    }
  },

  toggle: () => {
    const next: Theme = get().theme === 'dark' ? 'light' : 'dark';
    applyTheme(next);
    try {
      localStorage.setItem(storageKey, next);
    } catch {
      // localStorage unavailable (private mode, etc.); theme still applies
      // for this session, just won't persist.
    }
    set({ theme: next, explicit: true });
  },
}));
