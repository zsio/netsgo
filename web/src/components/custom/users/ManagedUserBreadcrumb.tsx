import { Link } from '@tanstack/react-router';
import { ChevronRight, Shield, Users } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { useManagedUser } from '@/hooks/use-users';

export function ManagedUserBreadcrumb({
  userId,
  clientLabel,
}: {
  userId: string;
  clientLabel?: string;
}) {
  const { t } = useTranslation();
  const user = useManagedUser(userId);
  const username = user.data?.username ?? userId.slice(0, 8);

  return (
    <div className="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-primary/20 bg-primary/5 px-3 py-2 text-xs">
      <nav aria-label="Breadcrumb" className="flex min-w-0 flex-wrap items-center gap-1.5 text-muted-foreground">
        <Link
          to="/dashboard/users"
          className="inline-flex items-center gap-1 font-medium text-muted-foreground transition-colors hover:text-foreground"
        >
          <Users className="h-3.5 w-3.5" />
          <span>{t('users.backToUsers')}</span>
        </Link>
        <ChevronRight className="h-3.5 w-3.5 shrink-0 text-muted-foreground/60" />
        {clientLabel ? (
          <>
            <Link
              to="/dashboard/users/$userId"
              params={{ userId }}
              className="truncate font-medium text-muted-foreground transition-colors hover:text-foreground"
            >
              {username}
            </Link>
            <ChevronRight className="h-3.5 w-3.5 shrink-0 text-muted-foreground/60" />
            <span className="truncate font-semibold text-foreground">{clientLabel}</span>
          </>
        ) : (
          <span className="truncate font-semibold text-foreground">{username}</span>
        )}
      </nav>
      <span className="inline-flex items-center gap-1 font-medium text-primary">
        <Shield className="h-3.5 w-3.5 shrink-0" />
        <span>{t('users.managedBanner', { username })}</span>
      </span>
    </div>
  );
}
