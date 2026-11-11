import { create } from 'zustand';

export type ThemePreference = 'light' | 'dark' | 'system';
export type ResolvedTheme = 'light' | 'dark';

export const THEME_STORAGE_KEY = 'netsgo-theme';

const THEME_CYCLE: readonly ThemePreference[] = ['system', 'light', 'dark'];

export function parseStoredTheme(raw: string | null | undefined): ThemePreference {
  if (raw === 'light' || raw === 'dark') {
    return raw;
  }
  return 'system';
}

export function nextThemePreference(current: ThemePreference): ThemePreference {
  const index = THEME_CYCLE.indexOf(current);
  return THEME_CYCLE[(index + 1) % THEME_CYCLE.length];
}

export function resolveEffectiveTheme(
  preference: ThemePreference,
  systemPrefersDark: boolean,
): ResolvedTheme {
  if (preference === 'system') {
    return systemPrefersDark ? 'dark' : 'light';
  }
  return preference;
}

function getSystemPrefersDark(): boolean {
  if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') {
    return false;
  }
  return window.matchMedia('(prefers-color-scheme: dark)').matches;
}

function readInitialPreference(): ThemePreference {
  if (typeof window === 'undefined') {
    return 'system';
  }
  try {
    return parseStoredTheme(window.localStorage.getItem(THEME_STORAGE_KEY));
  } catch {
    return 'system';
  }
}

function applyThemeToDOM(preference: ThemePreference) {
  if (typeof document === 'undefined') {
    return;
  }
  const effective = resolveEffectiveTheme(preference, getSystemPrefersDark());
  document.documentElement.classList.toggle('dark', effective === 'dark');
}

function persistThemePreference(preference: ThemePreference) {
  if (typeof window === 'undefined') {
    return;
  }
  try {
    if (preference === 'system') {
      window.localStorage.removeItem(THEME_STORAGE_KEY);
    } else {
      window.localStorage.setItem(THEME_STORAGE_KEY, preference);
    }
  } catch {
    // Ignore storage quota or private-mode restrictions
  }
}

interface ThemeState {
  preference: ThemePreference;
  resolved: ResolvedTheme;
  setPreference: (preference: ThemePreference) => void;
  cyclePreference: () => void;
}

const initialPreference = readInitialPreference();

export const useThemeStore = create<ThemeState>((set, get) => ({
  preference: initialPreference,
  resolved: resolveEffectiveTheme(initialPreference, getSystemPrefersDark()),
  setPreference: (preference) => {
    persistThemePreference(preference);
    applyThemeToDOM(preference);
    set({
      preference,
      resolved: resolveEffectiveTheme(preference, getSystemPrefersDark()),
    });
  },
  cyclePreference: () => {
    const next = nextThemePreference(get().preference);
    get().setPreference(next);
  },
}));

if (typeof window !== 'undefined' && typeof window.matchMedia === 'function') {
  const mediaQuery = window.matchMedia('(prefers-color-scheme: dark)');
  const handleChange = () => {
    const { preference } = useThemeStore.getState();
    if (preference === 'system') {
      applyThemeToDOM('system');
      useThemeStore.setState({
        resolved: resolveEffectiveTheme('system', mediaQuery.matches),
      });
    }
  };
  mediaQuery.addEventListener?.('change', handleChange);
}
