import { describe, expect, test } from 'bun:test';

import { describeFreshness, formatNetSpeed, formatTimestamp, bpsToMbpsInput, parseMbpsInputToBps } from './format';

describe('format helpers', () => {
  test('bpsToMbpsInput 转换 bytes/s 到最短可回填的 MiB/s 字符串', () => {
    expect(bpsToMbpsInput(0)).toBe('');
    expect(bpsToMbpsInput(null)).toBe('');
    expect(bpsToMbpsInput(undefined)).toBe('');
    expect(bpsToMbpsInput(1048576)).toBe('1');
    expect(bpsToMbpsInput(1572864)).toBe('1.5');
    expect(bpsToMbpsInput(100)).toBe('0.000095');
    expect(bpsToMbpsInput(1)).toBe('0.000001');
  });

  test('parseMbpsInputToBps 转换 MiB/s 字符串到 bytes/s 整数', () => {
    expect(parseMbpsInputToBps('')).toBe(0);
    expect(parseMbpsInputToBps('   ')).toBe(0);
    expect(parseMbpsInputToBps('invalid')).toBe(null);
    expect(parseMbpsInputToBps('-1')).toBe(null);
    expect(parseMbpsInputToBps('1')).toBe(1048576);
    expect(parseMbpsInputToBps('1.5')).toBe(1572864);
    expect(parseMbpsInputToBps('0.000095367431640625')).toBe(100);
    expect(parseMbpsInputToBps('0.000001')).toBe(1);
  });

  test('bpsToMbpsInput 与 parseMbpsInputToBps 可以无损往返小字节值', () => {
    const values = [1, 11, 32, 100, 119, 205, 1234, 5678, 1048577];

    for (const value of values) {
      const formatted = bpsToMbpsInput(value);
      expect(parseMbpsInputToBps(formatted)).toBe(value);
    }
  });

  test('formatNetSpeed 使用与带宽输入一致的二进制单位', () => {
    expect(formatNetSpeed(1024)).toBe('1.0 KiB/s');
    expect(formatNetSpeed(1048576)).toBe('1.0 MiB/s');
  });

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
