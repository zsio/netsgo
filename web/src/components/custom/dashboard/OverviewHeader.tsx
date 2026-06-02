import { useTranslation } from 'react-i18next';

export function OverviewHeader() {
  const { t } = useTranslation();

  return (
    <div className="flex flex-col gap-2">
      <h1 className="flex items-center gap-2 text-xl font-bold tracking-tight text-foreground sm:text-2xl">
        {t('common.dashboard')}
      </h1>
      <p className="flex items-center gap-2 text-sm text-muted-foreground">
        {t('dashboard.description')}
      </p>
    </div>
  );
}
