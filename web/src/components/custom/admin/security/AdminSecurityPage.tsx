import { useNavigate } from '@tanstack/react-router';
import toast from 'react-hot-toast';
import { useTranslation } from 'react-i18next';
import { useAdminSecurity } from '@/hooks/use-admin-security';
import { useAuthStore } from '@/stores/auth-store';
import { Skeleton } from '@/components/ui/skeleton';
import { AccountPasswordSection } from './AccountPasswordSection';
import { PasskeySection } from './PasskeySection';
import { TOTPSection } from './TOTPSection';

export function AdminSecurityPage() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const logout = useAuthStore((s) => s.logout);
  const { data, isLoading } = useAdminSecurity();

  const showSecurityError = (error: unknown) => {
    toast.error(error instanceof Error ? error.message : t('errors.generic'));
  };

  const forceRelogin = (message: string) => {
    toast.success(message);
    logout();
    void navigate({ to: '/login' });
  };

  const currentUser = data?.user;
  const requiresMFA = data?.totp_enabled ?? false;

  if (isLoading || !data || !currentUser) {
    return <SecuritySkeleton />;
  }

  return (
    <div className="flex flex-col gap-5 pb-10">
      <AccountPasswordSection
        currentUser={currentUser}
        requiresMFA={requiresMFA}
        onForceRelogin={forceRelogin}
        onError={showSecurityError}
      />

      <TOTPSection
        enabled={data.totp_enabled}
        recoveryCodesRemaining={data.recovery_codes_remaining}
        requiresMFA={requiresMFA}
        onForceRelogin={forceRelogin}
        onError={showSecurityError}
      />

      <PasskeySection
        passkeys={data.passkeys}
        requiresMFA={requiresMFA}
        onForceRelogin={forceRelogin}
        onError={showSecurityError}
      />
    </div>
  );
}

function SecuritySkeleton() {
  return (
    <div className="flex flex-col gap-5 pb-10">
      <Skeleton className="h-44 w-full rounded-xl" />
      <Skeleton className="h-40 w-full rounded-xl" />
      <Skeleton className="h-40 w-full rounded-xl" />
    </div>
  );
}
