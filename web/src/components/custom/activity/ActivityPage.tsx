import { useNavigate, useSearch } from '@tanstack/react-router';
import { motion } from 'motion/react';
import { useTranslation } from 'react-i18next';

import { ActivityFilters } from '@/components/custom/activity/ActivityFilters';
import type { ActivityFilterValue } from '@/components/custom/activity/severity-meta';
import { ActivityTimeline } from '@/components/custom/activity/ActivityTimeline';
import { adminUserResourceScope, SELF_RESOURCE_SCOPE } from '@/lib/resource-scope';
import { useAuthStore } from '@/stores/auth-store';
import { useAllUsers } from '@/hooks/use-users';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import type { ActivitySeverity } from '@/types';

const defaultSeverities: ActivitySeverity[] = ['info', 'warning', 'error'];

const fadeUp = {
  hidden: { opacity: 0, y: 12 },
  show: { opacity: 1, y: 0, transition: { duration: 0.35, ease: 'easeOut' as const } },
};

function dateBoundary(value: string | undefined, nextDay = false) {
  if (!value) return undefined;
  const date = new Date(`${value}T00:00:00`);
  if (nextDay) date.setDate(date.getDate() + 1);
  return date.toISOString();
}

export function ActivityPage() {
  const { t } = useTranslation();
  const principal = useAuthStore((state) => state.user);
  const search = useSearch({ from: '/dashboard/activity' });
  const navigate = useNavigate({ from: '/dashboard/activity' });
  const isAdmin = principal?.is_admin === true;
  const users = useAllUsers({ enabled: isAdmin });
  const readScope = isAdmin
    ? search.user_id
      ? adminUserResourceScope(search.user_id)
      : { kind: 'admin-global' as const }
    : SELF_RESOURCE_SCOPE;
  const scopeId = search.scope === 'client' ? search.client_id : search.scope === 'tunnel' ? search.tunnel_id : undefined;
  const query = {
    scope: search.scope,
    scopeId,
    limit: 50,
    severities: search.severity,
    categories: search.category,
    from: dateBoundary(search.from),
    to: dateBoundary(search.to, true),
  };
  const filters: ActivityFilterValue = {
    severities: search.severity,
    categories: search.category,
    fromDate: search.from,
    toDate: search.to,
  };
  const updateFilters = (next: ActivityFilterValue) => {
    navigate({
      search: (current) => ({
        ...current,
        severity: next.severities.length > 0 ? next.severities : defaultSeverities,
        category: next.categories,
        from: next.fromDate,
        to: next.toDate,
      }),
      replace: true,
    });
  };

  return (
    <motion.div
      variants={{ hidden: {}, show: { transition: { staggerChildren: 0.08 } } }}
      initial="hidden"
      animate="show"
      className="z-10 mx-auto flex w-full max-w-6xl flex-col gap-5 p-4 sm:gap-6 sm:p-6 lg:p-8"
    >
      <motion.div variants={fadeUp} className="flex flex-col gap-3">
        <div>
          <h3 className="text-xl font-semibold tracking-tight">{t('activity.pageTitle')}</h3>
          <p className="mt-1 text-sm text-muted-foreground">{t('activity.pageDescription')}</p>
        </div>
      </motion.div>
      <motion.div variants={fadeUp}>
        <section className="rounded-xl border border-border/40 bg-card/50 shadow-sm backdrop-blur-sm">
          <header className="rounded-t-xl border-b border-border/40 bg-muted/20 px-3 py-2.5 sm:px-4">
            <ActivityFilters
              value={filters}
              onChange={updateFilters}
              auxiliaryFilters={isAdmin ? (
                <Select
                  value={search.user_id ?? '__all__'}
                  onValueChange={(value) => {
                    navigate({
                      search: (current) => ({
                        ...current,
                        user_id: value === '__all__' ? undefined : value,
                      }),
                      replace: true,
                    });
                  }}
                >
                  <SelectTrigger size="sm" className="h-7 w-full text-xs shadow-none">
                    <SelectValue placeholder={t('activity.allUsers')} />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="__all__">{t('activity.allUsers')}</SelectItem>
                    {(users.data ?? []).map((user) => (
                      <SelectItem key={user.id} value={user.id}>{user.username}</SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              ) : undefined}
            />
          </header>
          <ActivityTimeline readScope={readScope} query={query} />
        </section>
      </motion.div>
    </motion.div>
  );
}
