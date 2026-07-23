import { Link } from '@tanstack/react-router';
import { Activity, ArrowRight } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import { ActivityTimeline } from './ActivityTimeline';
import { Button } from '@/components/ui/button';
import { useAuthStore } from '@/stores/auth-store';

export function ClientActivitySummary({ clientId }: { clientId: string }) {
  const { t } = useTranslation();
  const canReadActivity = useAuthStore((state) => state.user?.role === 'admin');
  if (!canReadActivity) return null;
  return (
    <section className="overflow-hidden rounded-xl border border-border/40 bg-card/50 shadow-sm backdrop-blur-sm">
      <header className="flex items-center justify-between gap-3 border-b border-border/40 bg-muted/20 px-4 py-3 sm:px-6 sm:py-4">
        <h2 className="flex items-center gap-2 font-semibold text-foreground"><Activity className="size-5 text-primary" />{t('activity.clientRecentTitle')}</h2>
        <Button variant="ghost" size="sm" asChild>
          <Link to="/dashboard/activity" search={{ scope: 'client', client_id: clientId, severity: ['info', 'warning', 'error'], category: [] }}>
            {t('activity.viewAll')}<ArrowRight />
          </Link>
        </Button>
      </header>
      <div className="p-4 sm:p-6">
        <ActivityTimeline compact query={{ scope: 'client', scopeId: clientId, limit: 10 }} />
      </div>
    </section>
  );
}
