import { describe, expect, test } from 'bun:test';

import { DEFAULT_LOCALE, LOCALE_STORAGE_KEY, resources, SUPPORTED_LOCALES } from '.';

describe('i18n', () => {
  test('defaults to English and has resources for every supported locale', () => {
    expect(DEFAULT_LOCALE).toBe('en-US');
    expect(LOCALE_STORAGE_KEY).toBe('netsgo.locale');
    expect(Object.keys(resources).sort()).toEqual([...SUPPORTED_LOCALES].sort());
  });
});
