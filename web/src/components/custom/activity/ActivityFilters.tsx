import { useTranslation } from 'react-i18next';

import { Checkbox } from '@/components/ui/checkbox';
import { Input } from '@/components/ui/input';
import type { ActivityCategory, ActivitySeverity } from '@/types';

const severities: ActivitySeverity[] = ['debug', 'info', 'warning', 'error'];
const categories: ActivityCategory[] = ['client', 'tunnel', 'p2p', 'admin', 'security'];

export interface ActivityFilterValue {
  severities: ActivitySeverity[];
  categories: ActivityCategory[];
  fromDate?: string;
  toDate?: string;
}

export function ActivityFilters({ value, onChange }: { value: ActivityFilterValue; onChange: (value: ActivityFilterValue) => void }) {
  const { t } = useTranslation();
  const toggle = <T extends string>(items: T[], item: T) => items.includes(item) ? items.filter((entry) => entry !== item) : [...items, item];

  return (
    <div className="rounded-xl border border-border/50 bg-card/50 p-4 shadow-sm">
      <div className="flex flex-wrap items-end gap-x-6 gap-y-4">
        <fieldset>
          <legend className="mb-2 text-xs font-medium uppercase tracking-wide text-muted-foreground">{t('activity.filterSeverity')}</legend>
          <div className="flex flex-wrap gap-3">
            {severities.map((severity) => (
              <label key={severity} className="flex cursor-pointer items-center gap-2 text-sm">
                <Checkbox checked={value.severities.includes(severity)} onCheckedChange={() => onChange({ ...value, severities: toggle(value.severities, severity) })} />
                {t(`activity.severity.${severity}`)}
              </label>
            ))}
          </div>
        </fieldset>
        <fieldset>
          <legend className="mb-2 text-xs font-medium uppercase tracking-wide text-muted-foreground">{t('activity.filterCategory')}</legend>
          <div className="flex flex-wrap gap-3">
            {categories.map((category) => (
              <label key={category} className="flex cursor-pointer items-center gap-2 text-sm">
                <Checkbox checked={value.categories.includes(category)} onCheckedChange={() => onChange({ ...value, categories: toggle(value.categories, category) })} />
                {t(`activity.category.${category}`)}
              </label>
            ))}
          </div>
        </fieldset>
        <label className="grid gap-1.5 text-xs font-medium uppercase tracking-wide text-muted-foreground">
          {t('activity.fromDate')}
          <Input type="date" value={value.fromDate ?? ''} onChange={(event) => onChange({ ...value, fromDate: event.target.value || undefined })} className="w-40 normal-case" />
        </label>
        <label className="grid gap-1.5 text-xs font-medium uppercase tracking-wide text-muted-foreground">
          {t('activity.toDate')}
          <Input type="date" value={value.toDate ?? ''} onChange={(event) => onChange({ ...value, toDate: event.target.value || undefined })} className="w-40 normal-case" />
        </label>
      </div>
    </div>
  );
}
