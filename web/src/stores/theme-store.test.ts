import { describe, expect, test } from 'vitest';

import {
  nextThemePreference,
  parseStoredTheme,
  resolveEffectiveTheme,
} from './theme-store';

describe('theme-store helpers', () => {
  test('parses stored theme safely and falls back to system', () => {
    expect(parseStoredTheme('light')).toBe('light');
    expect(parseStoredTheme('dark')).toBe('dark');
    expect(parseStoredTheme(null)).toBe('system');
    expect(parseStoredTheme('unexpected')).toBe('system');
  });

  test('resolves effective theme from preference and OS dark mode', () => {
    expect(resolveEffectiveTheme('light', true)).toBe('light');
    expect(resolveEffectiveTheme('dark', false)).toBe('dark');
    expect(resolveEffectiveTheme('system', true)).toBe('dark');
    expect(resolveEffectiveTheme('system', false)).toBe('light');
  });

  test('cycles through system -> light -> dark -> system', () => {
    expect(nextThemePreference('system')).toBe('light');
    expect(nextThemePreference('light')).toBe('dark');
    expect(nextThemePreference('dark')).toBe('system');
  });
});
