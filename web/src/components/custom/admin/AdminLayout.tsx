import { Link, Outlet, useRouterState } from '@tanstack/react-router';
import { motion } from 'motion/react';
import { ShieldAlert, ShieldCheck, SlidersHorizontal } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs';

export function AdminLayout() {
  const { t } = useTranslation();
  const pathname = useRouterState({ select: (s) => s.location.pathname });
  const activeTab = pathname.includes('/security')
    ? 'security'
    : pathname.includes('/access-control')
      ? 'access-control'
      : 'config';

  return (
    <motion.div
      className="z-10 mx-auto flex w-full max-w-5xl flex-col gap-5 p-4 sm:gap-6 sm:p-6 lg:p-8"
      initial={{ opacity: 0, y: 10 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ duration: 0.25, ease: 'easeOut' as const }}
    >
      <div className="flex flex-col gap-4">
        <div>
          <h3 className="text-xl font-semibold tracking-tight">{t('admin.systemSettingsTitle')}</h3>
          <p className="mt-1 text-sm text-muted-foreground">{t('admin.systemSettingsDescription')}</p>
        </div>
        <Tabs value={activeTab} className="w-full">
          <TabsList className="h-9 w-full justify-start sm:w-fit">
            <TabsTrigger value="config" asChild className="px-3 data-active:text-brand-strong data-active:[&_svg]:text-primary">
              <Link to="/dashboard/admin/config">
                <SlidersHorizontal data-icon="inline-start" />
                <span>{t('admin.serviceConfigTab')}</span>
              </Link>
            </TabsTrigger>
            <TabsTrigger value="access-control" asChild className="px-3 data-active:text-brand-strong data-active:[&_svg]:text-primary">
              <Link to="/dashboard/admin/access-control">
                <ShieldAlert data-icon="inline-start" />
                <span>{t('admin.accessControlTab')}</span>
              </Link>
            </TabsTrigger>
            <TabsTrigger value="security" asChild className="px-3 data-active:text-brand-strong data-active:[&_svg]:text-primary">
              <Link to="/dashboard/admin/security">
                <ShieldCheck data-icon="inline-start" />
                <span>{t('admin.securityTab')}</span>
              </Link>
            </TabsTrigger>
          </TabsList>
        </Tabs>
      </div>
      <Outlet />
    </motion.div>
  );
}
