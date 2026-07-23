import { AlertTriangle, ArrowRight, Bug, Calendar, CircleAlert, Info, Monitor, Network, RotateCcw, ShieldAlert, UserCog, Waypoints } from 'lucide-react';
import type { LucideIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { cn } from '@/lib/utils';
import type { ActivityCategory, ActivitySeverity } from '@/types';

const severities: ActivitySeverity[] = ['debug', 'info', 'warning', 'error'];
const categories: ActivityCategory[] = ['client', 'tunnel', 'p2p', 'admin', 'security'];

const severityIcon: Record<ActivitySeverity, LucideIcon> = {
  debug: Bug,
  info: Info,
  warning: AlertTriangle,
  error: CircleAlert,
};

const severityActiveClass: Record<ActivitySeverity, string> = {
  debug: 'border-slate-400/40 bg-slate-500/10 text-slate-600 dark:text-slate-300',
  info: 'border-sky-400/40 bg-sky-500/10 text-sky-600 dark:text-sky-300',
  warning: 'border-amber-400/40 bg-amber-500/10 text-amber-600 dark:text-amber-300',
  error: 'border-rose-400/40 bg-rose-500/10 text-rose-600 dark:text-rose-300',
};

const categoryIcon: Record<ActivityCategory, LucideIcon> = {
  client: Monitor,
  tunnel: Waypoints,
  p2p: Network,
  admin: UserCog,
  security: ShieldAlert,
};

export interface ActivityFilterValue {
  severities: ActivitySeverity[];
  categories: ActivityCategory[];
  fromDate?: string;
  toDate?: string;
}

const defaultActivityFilter: ActivityFilterValue = {
  severities: ['info', 'warning', 'error'],
  categories: [],
};

function FilterChip({ active, activeClass, icon: Icon, label, onToggle }: {
  active: boolean;
  activeClass?: string;
  icon: LucideIcon;
  label: string;
  onToggle: () => void;
}) {
  return (
    <button
      type="button"
      aria-pressed={active}
      onClick={onToggle}
      className={cn(
        'inline-flex h-7 items-center gap-1.5 rounded-full border px-2.5 text-[11px] font-medium tracking-wide transition-all duration-200',
        active
          ? (activeClass ?? 'border-primary/40 bg-primary/10 text-primary')
          : 'border-border/60 text-muted-foreground hover:border-foreground/30 hover:text-foreground',
      )}
    >
      <Icon className="size-3" strokeWidth={2.25} />
      {label}
    </button>
  );
}

export function ActivityFilters({ value, onChange }: { value: ActivityFilterValue; onChange: (value: ActivityFilterValue) => void }) {
  const { t } = useTranslation();
  const toggle = <T extends string>(items: T[], item: T) => items.includes(item) ? items.filter((entry) => entry !== item) : [...items, item];

  return (
    <div className="rounded-xl border border-border/40 bg-card/50 p-4 shadow-sm backdrop-blur-sm">
      <div className="flex flex-wrap items-center gap-x-5 gap-y-3">
      <span className="text-[10px] font-semibold uppercase tracking-[0.2em] text-muted-foreground/70">{t('activity.filterSeverity')}</span>
      <div className="flex flex-wrap gap-1.5">
        {severities.map((severity) => (
          <FilterChip
            key={severity}
            active={value.severities.includes(severity)}
            activeClass={severityActiveClass[severity]}
            icon={severityIcon[severity]}
            label={t(`activity.severity.${severity}`)}
            onToggle={() => onChange({ ...value, severities: toggle(value.severities, severity) })}
          />
        ))}
      </div>
      <span className="hidden h-5 w-px bg-border/60 sm:block" />
      <span className="text-[10px] font-semibold uppercase tracking-[0.2em] text-muted-foreground/70">{t('activity.filterCategory')}</span>
      <div className="flex flex-wrap gap-1.5">
        {categories.map((category) => (
          <FilterChip
            key={category}
            active={value.categories.includes(category)}
            icon={categoryIcon[category]}
            label={t(`activity.category.${category}`)}
            onToggle={() => onChange({ ...value, categories: toggle(value.categories, category) })}
          />
        ))}
      </div>
      <div className="ms-auto flex items-center gap-2">
        <label className="relative block">
          <Calendar className="pointer-events-none absolute left-2.5 top-1/2 size-3 -translate-y-1/2 text-muted-foreground" />
          <Input
            type="date"
            aria-label={t('activity.fromDate')}
            value={value.fromDate ?? ''}
            onChange={(event) => onChange({ ...value, fromDate: event.target.value || undefined })}
            className="h-7 w-36 border-border/60 bg-transparent pl-7 text-xs shadow-none"
          />
        </label>
        <ArrowRight className="size-3 text-muted-foreground/60" />
        <label className="relative block">
          <Calendar className="pointer-events-none absolute left-2.5 top-1/2 size-3 -translate-y-1/2 text-muted-foreground" />
          <Input
            type="date"
            aria-label={t('activity.toDate')}
            value={value.toDate ?? ''}
            onChange={(event) => onChange({ ...value, toDate: event.target.value || undefined })}
            className="h-7 w-36 border-border/60 bg-transparent pl-7 text-xs shadow-none"
          />
        </label>
        <Button
          variant="ghost"
          size="icon"
          className="size-7 text-muted-foreground hover:text-foreground"
          title={t('activity.filterReset')}
          onClick={() => onChange({ ...defaultActivityFilter })}
        >
          <RotateCcw className="size-3.5" />
        </Button>
      </div>
      </div>
    </div>
  );
}
