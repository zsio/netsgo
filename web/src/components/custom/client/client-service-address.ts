import { normalizeServerAddr } from '@/lib/server-address';

interface AddClientServiceAddressSources {
  effectiveServerAddr?: string;
  adminServerAddr?: string;
  keyServerAddr?: string;
  statusServerAddr?: string;
  browserOrigin: string;
}

export function resolveAddClientServiceAddress(sources: AddClientServiceAddressSources) {
  const candidates = [
    sources.effectiveServerAddr,
    sources.adminServerAddr,
    sources.keyServerAddr,
    sources.statusServerAddr,
    sources.browserOrigin,
  ];

  for (const candidate of candidates) {
    if (!candidate) continue;

    const normalized = normalizeServerAddr(candidate);
    if (normalized) return normalized;
  }

  return sources.browserOrigin.trim();
}
