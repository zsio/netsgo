import { createRoute, Link, Outlet, redirect, useRouterState } from '@tanstack/react-router';
import { motion } from 'motion/react';
import { Shield, SlidersHorizontal } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { dashboardRoute } from './dashboard';
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { cn } from '@/lib/utils';

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
      className="z-10 mx-auto flex w-full max-w-6xl flex-col gap-6 p-4 sm:p-6 lg:p-8"
      initial={{ opacity: 0, y: 10 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ duration: 0.3, ease: 'easeOut' as const }}
    >
      <div className="border-b border-border/60">
        <div className="flex flex-col gap-5">
          <div>
            <h2 className="text-3xl font-bold tracking-tight">{t('admin.systemSettingsTitle')}</h2>
            <p className="mt-2 max-w-2xl text-sm font-medium text-muted-foreground">{t('admin.systemSettingsDescription')}</p>
          </div>
          <Tabs value={activeTab} className="w-full">
            <TabsList variant="line" className="h-12 w-full justify-start gap-8 rounded-none p-0">
              <AdminTabTrigger
                active={activeTab === 'config'}
                icon={SlidersHorizontal}
                label={t('admin.serviceConfigTab')}
                to="/dashboard/admin/config"
                value="config"
              />
              <AdminTabTrigger
                active={activeTab === 'security'}
                icon={Shield}
                label={t('admin.securityTab')}
                to="/dashboard/admin/security"
                value="security"
              />
            </TabsList>
          </Tabs>
        </div>
      </div>
      <Outlet />
    </motion.div>
  );
}

function AdminTabTrigger({
  active,
  icon: Icon,
  label,
  to,
  value,
}: {
  active: boolean;
  icon: React.ComponentType<React.SVGProps<SVGSVGElement>>;
  label: string;
  to: string;
  value: string;
}) {
  return (
    <TabsTrigger value={value} asChild>
      <Link
        to={to}
        className={cn(
          'relative h-12 flex-none rounded-none px-0 text-sm font-semibold transition-colors',
          'after:absolute after:inset-x-0 after:bottom-0 after:h-0.5 after:rounded-full after:transition-opacity',
          active
            ? 'text-primary after:bg-primary after:opacity-100'
            : 'text-muted-foreground after:bg-transparent after:opacity-0 hover:text-foreground',
        )}
      >
        <Icon data-icon="inline-start" />
        {label}
      </Link>
    </TabsTrigger>
  );
}
