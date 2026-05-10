import type { VersionCheckResult } from '@/hooks/use-version-check';

export function manualVersionCheckToast(result: VersionCheckResult | null, errored = false): 'error' | 'success' | null {
  if (errored) return 'error';
  if (!result || result.update_available) return null;
  if (result.check_failed || (result.refresh_failed && result.cache_source === 'stale_cache')) return 'error';
  return 'success';
}
