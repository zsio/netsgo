import { useTranslation } from 'react-i18next';

import { ActivityTimeline } from './ActivityTimeline';
import { ScrollArea } from '@/components/ui/scroll-area';
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from '@/components/ui/sheet';

export interface ActivityTimelineSheetTarget {
  id: string;
  name?: string;
}

export function ActivityTimelineSheet({ target, onOpenChange }: { target: ActivityTimelineSheetTarget | null; onOpenChange: (open: boolean) => void }) {
  const { t } = useTranslation();
  return (
    <Sheet open={Boolean(target)} onOpenChange={onOpenChange}>
      <SheetContent className="w-[min(92vw,44rem)] sm:max-w-none">
        <SheetHeader className="border-b border-border/50 pr-12">
          <SheetTitle>{t('activity.tunnelTimelineTitle', { name: target?.name || target?.id })}</SheetTitle>
          <SheetDescription>{t('activity.tunnelTimelineDescription')}</SheetDescription>
        </SheetHeader>
        <ScrollArea className="min-h-0 flex-1 px-4 pb-6">
          {target ? <ActivityTimeline query={{ scope: 'tunnel', scopeId: target.id, limit: 50 }} /> : null}
        </ScrollArea>
      </SheetContent>
    </Sheet>
  );
}
