import { AlertCircle } from 'lucide-react';
import { Button } from '@/components/ui/button';

interface ErrorFallbackProps {
  error?: Error;
  onRetry?: () => void;
}

export function ErrorFallback({ error, onRetry }: ErrorFallbackProps) {
  return (
    <div className="flex-1 flex flex-col items-center justify-center text-muted-foreground p-8">
      <AlertCircle className="h-16 w-16 mb-4 opacity-20 text-destructive" />
      <p className="text-lg font-medium">无法连接到服务端</p>
      <p className="text-sm opacity-60 mt-2 max-w-md text-center">
        {error?.message || '请确认 NetsGo Server 正在运行，然后重试。'}
      </p>
      {onRetry && (
        <Button variant="outline" className="mt-6" onClick={onRetry}>
          重新连接
        </Button>
      )}
    </div>
  );
}
