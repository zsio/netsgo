import { ChevronLeft, ChevronRight } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/button';
import { clampTablePage, DEFAULT_TABLE_PAGE_SIZE } from './table-pagination';

export function TablePaginationBar({
  totalItems,
  page,
  pageSize = DEFAULT_TABLE_PAGE_SIZE,
  onPageChange,
}: {
  totalItems: number;
  page: number;
  pageSize?: number;
  onPageChange: (nextPage: number) => void;
}) {
  const { t } = useTranslation();

  if (totalItems <= pageSize) {
    return null;
  }

  const totalPages = Math.max(1, Math.ceil(totalItems / pageSize));
  const currentPage = clampTablePage(page, totalItems, pageSize);
  const startIndex = (currentPage - 1) * pageSize + 1;
  const endIndex = Math.min(totalItems, currentPage * pageSize);

  return (
    <div className="flex items-center justify-between gap-3 border-t border-border/40 bg-muted/10 px-4 py-2.5 text-xs text-muted-foreground sm:px-6">
      <span>
        {t('common.paginationRange', {
          start: startIndex,
          end: endIndex,
          total: totalItems,
        })}
      </span>
      <div className="flex items-center gap-1.5">
        <Button
          type="button"
          variant="outline"
          size="sm"
          className="h-7 gap-1 px-2 text-xs"
          disabled={currentPage <= 1}
          onClick={() => onPageChange(currentPage - 1)}
        >
          <ChevronLeft className="h-3.5 w-3.5" />
          {t('common.previous')}
        </Button>
        <Button
          type="button"
          variant="outline"
          size="sm"
          className="h-7 gap-1 px-2 text-xs"
          disabled={currentPage >= totalPages}
          onClick={() => onPageChange(currentPage + 1)}
        >
          {t('common.next')}
          <ChevronRight className="h-3.5 w-3.5" />
        </Button>
      </div>
    </div>
  );
}
