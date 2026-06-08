import { createRoute, Link, Outlet, redirect, useRouterState } from '@tanstack/react-router';
import { motion } from 'motion/react';
import { Shield, SlidersHorizontal } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { dashboardRoute } from './dashboard';
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs';

export const adminRoute = createRoute({
  getParentRoute: () => dashboardRoute,
  path: '/admin',
  component: AdminLayout,
});

export const adminIndexRoute = createRoute({
  getParentRoute: () => adminRoute,
  path: '/',
  beforeLoad: () => {
    throw redirect({ to: '/dashboard/admin/config' });
  },
});

function AdminLayout() {
  const { t } = useTranslation();
  const pathname = useRouterState({ select: (s) => s.location.pathname });
  const activeTab = pathname.includes('/security') ? 'security' : 'config';

  return (
    <motion.div
      className="z-10 mx-auto flex w-full max-w-6xl flex-col gap-5 p-4 sm:gap-6 sm:p-6 lg:p-8"
      initial={{ opacity: 0, y: 10 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ duration: 0.3, ease: 'easeOut' as const }}
    >
      <div className="flex flex-col gap-3">
        <div>
          <h2 className="text-2xl font-bold tracking-tight">{t('admin.systemSettingsTitle')}</h2>
          <p className="text-sm text-muted-foreground mt-1">{t('admin.systemSettingsDescription')}</p>
        </div>
        <Tabs value={activeTab} className="w-full">
          <TabsList variant="line" className="w-full justify-start">
            <TabsTrigger value="config" asChild>
              <Link to="/dashboard/admin/config">
                <SlidersHorizontal data-icon="inline-start" />
                {t('admin.serviceConfigTab')}
              </Link>
            </TabsTrigger>
            <TabsTrigger value="security" asChild>
              <Link to="/dashboard/admin/security">
                <Shield data-icon="inline-start" />
                {t('admin.securityTab')}
              </Link>
            </TabsTrigger>
          </TabsList>
        </Tabs>
      </div>
      <Outlet />
    </motion.div>
  );
}
