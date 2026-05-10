import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from '@/lib/api';

export type VersionInstallMethod = 'service' | 'docker' | 'binary';
export type VersionTargetKind = 'server' | 'client';

export interface VersionCheckCommands {
  domestic: string;
  global: string;
}

export interface VersionCheckResult {
  target: VersionTargetKind;
  target_id: string;
  current_version: string;
  latest_version: string;
  update_available: boolean;
  checked_at: string;
  install_method: VersionInstallMethod;
  recommended_channel: 'stable' | 'beta' | '';
  recommended_action: 'none' | 'run_script' | 'github_release' | 'docker_docs';
  commands: VersionCheckCommands | null;
  release_url: string;
  check_failed: boolean;
  refresh_failed: boolean;
  cache_source: 'fresh' | 'cache' | 'stale_cache' | 'none';
  reason: string;
}

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
