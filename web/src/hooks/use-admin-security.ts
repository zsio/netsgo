import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from '@/lib/api';
import type {
  AdminSecurity,
  PasskeyChallengeResponse,
  RecoveryCodesResponse,
  ReloginResponse,
  TOTPBeginResponse,
} from '@/types';

export const adminSecurityQueryKey = ['admin-security'];

export function useAdminSecurity() {
  return useQuery({
    queryKey: adminSecurityQueryKey,
    queryFn: () => api.get<AdminSecurity>('/api/admin/security'),
  });
}

export function useAdminSecurityMutations() {
  const queryClient = useQueryClient();
  const invalidate = () => queryClient.invalidateQueries({ queryKey: adminSecurityQueryKey });

  return {
    updateUsername: useMutation({
      mutationFn: (body: { current_password: string; new_username: string; mfa_code?: string }) =>
        api.put<ReloginResponse>('/api/admin/security/username', body),
      onSuccess: invalidate,
    }),
    updatePassword: useMutation({
      mutationFn: (body: { current_password: string; new_password: string; mfa_code?: string }) =>
        api.put<ReloginResponse>('/api/admin/security/password', body),
      onSuccess: invalidate,
    }),
    beginTOTP: useMutation({
      mutationFn: (body: { current_password: string; mfa_code?: string }) =>
        api.post<TOTPBeginResponse>('/api/admin/security/totp/begin', body),
    }),
    confirmTOTP: useMutation({
      mutationFn: (body: { setup_token: string; code: string }) =>
        api.post<RecoveryCodesResponse>('/api/admin/security/totp/confirm', body),
      onSuccess: invalidate,
    }),
    disableTOTP: useMutation({
      mutationFn: (body: { current_password: string; mfa_code?: string }) =>
        api.delete<ReloginResponse>('/api/admin/security/totp', body),
      onSuccess: invalidate,
    }),
    regenerateRecoveryCodes: useMutation({
      mutationFn: (body: { current_password: string; mfa_code?: string }) =>
        api.post<RecoveryCodesResponse>('/api/admin/security/recovery-codes/regenerate', body),
      onSuccess: invalidate,
    }),
    beginPasskey: useMutation({
      mutationFn: (body: { current_password: string; mfa_code?: string; name: string }) =>
        api.post<PasskeyChallengeResponse>('/api/admin/security/passkeys/begin', body),
    }),
    finishPasskey: useMutation({
      mutationFn: (body: { challenge_id: string; credential: unknown }) =>
        api.post<ReloginResponse>('/api/admin/security/passkeys/finish', body),
      onSuccess: invalidate,
    }),
    renamePasskey: useMutation({
      mutationFn: (body: { id: string; current_password: string; mfa_code?: string; name: string }) =>
        api.put(`/api/admin/security/passkeys/${encodeURIComponent(body.id)}`, {
          current_password: body.current_password,
          mfa_code: body.mfa_code,
          name: body.name,
        }),
      onSuccess: invalidate,
    }),
    deletePasskey: useMutation({
      mutationFn: (body: { id: string; current_password: string; mfa_code?: string }) =>
        api.delete<ReloginResponse>(`/api/admin/security/passkeys/${encodeURIComponent(body.id)}`, {
          current_password: body.current_password,
          mfa_code: body.mfa_code,
        }),
      onSuccess: invalidate,
    }),
  };
}
