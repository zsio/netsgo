import { describe, expect, test } from 'bun:test';

import { describeFreshness, formatTimestamp } from './format';

describe('format helpers', () => {
  test('formatTimestamp 对缺失或非法输入返回占位符', () => {
    expect(formatTimestamp()).toBe('-');
    expect(formatTimestamp('not-a-date')).toBe('-');
  });

  test('formatTimestamp 对合法时间返回可读字符串', () => {
    expect(formatTimestamp('2026-03-27T09:00:00Z')).not.toBe('-');
  });

  test('describeFreshness 对缺失或非法时间返回未知', () => {
    expect(describeFreshness()).toBe('时间未知');
    expect(describeFreshness('not-a-date')).toBe('时间未知');
  });

  test('describeFreshness 在 fresh_until 过期时直接提示已过期', () => {
    const updatedAt = new Date(Date.now() - 10_000).toISOString();
    const freshUntil = new Date(Date.now() - 1_000).toISOString();

    expect(describeFreshness(updatedAt, freshUntil)).toBe('可能已过期');
  });

  test('describeFreshness 对新鲜数据返回相对时间', () => {
    const updatedAt = new Date(Date.now() - 5_000).toISOString();
    const freshUntil = new Date(Date.now() + 5_000).toISOString();

    expect(describeFreshness(updatedAt, freshUntil)).toEndWith('秒前更新');
  });
});
