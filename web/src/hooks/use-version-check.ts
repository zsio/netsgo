import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from '@/lib/api';
import type { VersionCheckResult, VersionInstallMethod, VersionTargetKind } from '@/types';

export interface VersionCheckTarget {
  kind: VersionTargetKind;
  id?: string;
  version?: string;
  installMethod?: VersionInstallMethod;
  os?: string;
  arch?: string;
  enabled?: boolean;
}

function normalizeMethod(method?: string): VersionInstallMethod {
  return method === 'service' || method === 'docker' || method === 'binary' ? method : 'binary';
}

export function versionCheckQueryKey(target: VersionCheckTarget) {
  return [
    'version-check',
    target.kind,
    target.id || target.kind,
    target.version || '',
    normalizeMethod(target.installMethod),
    target.os || '',
    target.arch || '',
  ] as const;
}

function endpoint(target: VersionCheckTarget, force: boolean) {
  const qs = force ? '?force=true' : '';
  if (target.kind === 'client') {
    return `/api/clients/${encodeURIComponent(target.id || '')}/version/check${qs}`;
  }
  return `/api/version/check${qs}`;
}

export function useVersionCheck(target: VersionCheckTarget) {
  const enabled = Boolean(target.enabled ?? true) && Boolean(target.version) && (target.kind === 'server' || Boolean(target.id));

  return useQuery({
    queryKey: versionCheckQueryKey(target),
    queryFn: () => api.get<VersionCheckResult>(endpoint(target, false)),
    enabled,
    retry: false,
    staleTime: 10 * 60 * 1000,
  });
}

export function useForceVersionCheck(target: VersionCheckTarget) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: () => api.get<VersionCheckResult>(endpoint(target, true)),
    onSuccess: (result) => {
      queryClient.setQueryData(versionCheckQueryKey(target), result);
    },
  });
}
