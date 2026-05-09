import { useQuery } from '@tanstack/react-query';
import { api } from '@/lib/api';

const VERSION_CHECK_TTL_MS = 60 * 60 * 1000;
const STORAGE_PREFIX = 'netsgo.version-check.';

export type VersionInstallMethod = 'cli' | 'docker' | 'service';

export interface VersionCheckResult {
  current_version: string;
  latest_version: string;
  update_available: boolean;
  checked_at: string;
  install_method?: VersionInstallMethod;
  mocked?: boolean;
}

interface CachedVersionCheck {
  checkedAt: number;
  result?: VersionCheckResult;
}

function normalizeVersion(version?: string): string {
  return (version || '').trim().replace(/^v/, '');
}

function cacheKey(version: string): string {
  return `${STORAGE_PREFIX}${normalizeVersion(version)}`;
}

function readCachedVersionCheck(version: string): CachedVersionCheck | undefined {
  if (typeof window === 'undefined') return undefined;
  const normalized = normalizeVersion(version);
  if (!normalized) return undefined;

  try {
    const raw = window.localStorage.getItem(cacheKey(normalized));
    if (!raw) return undefined;
    const cached = JSON.parse(raw) as CachedVersionCheck;
    if (!cached?.checkedAt) return undefined;
    if (Date.now() - cached.checkedAt > VERSION_CHECK_TTL_MS) return undefined;
    return cached;
  } catch {
    return undefined;
  }
}

function writeCachedVersionCheck(version: string, result?: VersionCheckResult) {
  if (typeof window === 'undefined') return;
  const normalized = normalizeVersion(version);
  if (!normalized) return;

  const cached: CachedVersionCheck = {
    checkedAt: Date.now(),
    result,
  };
  window.localStorage.setItem(cacheKey(normalized), JSON.stringify(cached));
}

export function useVersionCheck(version?: string) {
  const normalized = normalizeVersion(version);

  return useQuery({
    queryKey: ['version-check', normalized],
    queryFn: async () => {
      const cached = readCachedVersionCheck(normalized);
      if (cached) return cached.result;

      try {
        const result = await api.get<VersionCheckResult>(`/api/version/check?version=${encodeURIComponent(normalized)}`);
        writeCachedVersionCheck(normalized, result);
        return result;
      } catch (error) {
        writeCachedVersionCheck(normalized);
        throw error;
      }
    },
    enabled: normalized.length > 0,
    retry: false,
    staleTime: VERSION_CHECK_TTL_MS,
    gcTime: VERSION_CHECK_TTL_MS,
  });
}
