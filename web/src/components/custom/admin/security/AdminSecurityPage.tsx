import { type ComponentType, type SVGProps, useState } from 'react';
import { useNavigate } from '@tanstack/react-router';
import { Fingerprint, ShieldCheck, UserRound, Webhook } from 'lucide-react';
import toast from 'react-hot-toast';
import { useTranslation } from 'react-i18next';
import { useAdminSecurity } from '@/hooks/use-admin-security';
import { useAuthStore } from '@/stores/auth-store';
import { Skeleton } from '@/components/ui/skeleton';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { AccountPasswordSection } from './AccountPasswordSection';
import { PasskeySection } from './PasskeySection';
import { TOTPSection } from './TOTPSection';
import { WebhookDeliverySection } from './WebhookDeliverySection';

export function AdminSecurityPage() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const logout = useAuthStore((s) => s.logout);
  const { data, isLoading } = useAdminSecurity();
  const [securitySection, setSecuritySection] = useState('account');

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
    <div className="pb-10">
      <Tabs value={securitySection} onValueChange={setSecuritySection}>
        <TabsList>
          <SecuritySectionTrigger value="account" icon={UserRound} title={t('admin.accountPasswordTab')} />
          <SecuritySectionTrigger value="totp" icon={ShieldCheck} title={t('admin.twoFactorAuth')} />
          <SecuritySectionTrigger value="passkey" icon={Fingerprint} title={t('admin.passkeys')} />
          <SecuritySectionTrigger value="webhook" icon={Webhook} title={t('admin.webhookDeliveryTab')} />
        </TabsList>

        <TabsContent value="account" className="mt-0">
          <AccountPasswordSection
            currentUser={currentUser}
            requiresMFA={requiresMFA}
            onForceRelogin={forceRelogin}
            onError={showSecurityError}
          />
        </TabsContent>

        <TabsContent value="totp" className="mt-0">
          <TOTPSection
            enabled={data.totp_enabled}
            recoveryCodesRemaining={data.recovery_codes_remaining}
            requiresMFA={requiresMFA}
            onForceRelogin={forceRelogin}
            onError={showSecurityError}
          />
        </TabsContent>

        <TabsContent value="passkey" className="mt-0">
          <PasskeySection
            passkeys={data.passkeys}
            requiresMFA={requiresMFA}
            onForceRelogin={forceRelogin}
            onError={showSecurityError}
          />
        </TabsContent>

        <TabsContent value="webhook" className="mt-0">
          <WebhookDeliverySection />
        </TabsContent>
      </Tabs>
    </div>
  );
}

function SecuritySkeleton() {
  return (
    <div className="flex flex-col gap-5 pb-10">
      <Skeleton className="h-10 w-full max-w-md rounded-lg" />
      <Skeleton className="h-[360px] w-full rounded-xl" />
    </div>
  );
}

function SecuritySectionTrigger({
  value,
  icon: Icon,
  title,
  description,
}: {
  value: string;
  icon: ComponentType<SVGProps<SVGSVGElement>>;
  title: string;
  description?: string;
}) {
  return (
    <TabsTrigger value={value}>
      <Icon data-icon="inline-start" />
      <span className="truncate">{title}</span>
      {description ? <span className="sr-only">{description}</span> : null}
    </TabsTrigger>
  );
}
