import { AlertCircle } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { useTranslation } from 'react-i18next';

interface ErrorFallbackProps {
  error?: Error;
  onRetry?: () => void;
}

export function ErrorFallback({ error, onRetry }: ErrorFallbackProps) {
  const { t } = useTranslation();

  return (
    <div className="flex-1 flex flex-col items-center justify-center text-muted-foreground p-8">
      <AlertCircle className="h-16 w-16 mb-4 opacity-20 text-destructive" />
      <p className="text-lg font-medium">{t('errors.server_unreachable')}</p>
      <p className="text-sm opacity-60 mt-2 max-w-md text-center">
        {error?.message || t('errors.server_unreachable_description')}
      </p>
      {onRetry && (
        <Button variant="outline" className="mt-6" onClick={onRetry}>
          {t('common.retry')}
        </Button>
      )}
    </div>
  );
}
