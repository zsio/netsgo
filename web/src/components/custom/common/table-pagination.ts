export const DEFAULT_TABLE_PAGE_SIZE = 50;

export function clampTablePage(page: number, totalItems: number, pageSize = DEFAULT_TABLE_PAGE_SIZE) {
  const totalPages = Math.max(1, Math.ceil(totalItems / pageSize));
  return Math.min(Math.max(1, page), totalPages);
}

export function sliceTablePage<T>(
  items: readonly T[],
  page: number,
  pageSize = DEFAULT_TABLE_PAGE_SIZE,
): { pageItems: T[]; currentPage: number; totalPages: number; startIndex: number; endIndex: number } {
  const totalPages = Math.max(1, Math.ceil(items.length / pageSize));
  const currentPage = clampTablePage(page, items.length, pageSize);
  const startOffset = (currentPage - 1) * pageSize;
  const pageItems = items.slice(startOffset, startOffset + pageSize);
  return {
    pageItems,
    currentPage,
    totalPages,
    startIndex: items.length === 0 ? 0 : startOffset + 1,
    endIndex: startOffset + pageItems.length,
  };
}
