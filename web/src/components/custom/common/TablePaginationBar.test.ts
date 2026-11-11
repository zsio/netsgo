import { describe, expect, test } from 'vitest';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';

import { clampTablePage, sliceTablePage } from '@/components/custom/common/table-pagination';
import { TablePaginationBar } from '@/components/custom/common/TablePaginationBar';
import { i18n } from '@/i18n';

describe('TablePaginationBar & pagination helpers', () => {
  test('slices items safely and clamps out-of-range pages', () => {
    const items = Array.from({ length: 120 }, (_, i) => i + 1);

    expect(clampTablePage(0, 120, 50)).toBe(1);
    expect(clampTablePage(9, 120, 50)).toBe(3);

    const lastPage = sliceTablePage(items, 9, 50);
    expect(lastPage.currentPage).toBe(3);
    expect(lastPage.totalPages).toBe(3);
    expect(lastPage.startIndex).toBe(101);
    expect(lastPage.endIndex).toBe(120);
    expect(lastPage.pageItems).toHaveLength(20);
  });

  test('renders nothing when total items fit within one page', () => {
    const markup = renderToStaticMarkup(
      createElement(TablePaginationBar, {
        totalItems: 50,
        page: 1,
        pageSize: 50,
        onPageChange: () => undefined,
      }),
    );
    expect(markup).toBe('');
  });

  test('renders localized range and navigation buttons when exceeding page size', async () => {
    await i18n.changeLanguage('en-US');
    const markup = renderToStaticMarkup(
      createElement(TablePaginationBar, {
        totalItems: 125,
        page: 2,
        pageSize: 50,
        onPageChange: () => undefined,
      }),
    );
    expect(markup).toContain('51–100 of 125');
    expect(markup).toContain('Previous');
    expect(markup).toContain('Next');
  });
});
