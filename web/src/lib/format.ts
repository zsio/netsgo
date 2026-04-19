/**
 * 数据格式化工具
 * 将后端返回的原始数据转换为人类可读格式
 */

const UNITS = ["B", "KB", "MB", "GB", "TB"] as const;
const BYTES_PER_MEGABYTE = 1000 * 1000;

/** 将字节数转换为人类可读格式: 1073741824 → "1.0 GB" */
export function formatBytes(bytes: number): string {
  if (bytes === 0) return "0 B";
  const i = Math.floor(Math.log(bytes) / Math.log(1024));
  const idx = Math.min(i, UNITS.length - 1);
  return `${(bytes / Math.pow(1024, idx)).toFixed(1)} ${UNITS[idx]}`;
}

/** 将秒数转换为可读运行时间: 90061 → "1 天 1 小时" */
export function formatUptime(seconds: number): string {
  if (seconds < 60) return `${seconds} 秒`;

  const years = Math.floor(seconds / (86400 * 365));
  const days = Math.floor((seconds % (86400 * 365)) / 86400);
  const hours = Math.floor((seconds % 86400) / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);

  if (years > 0) return `${years} 年 ${days} 天`;
  if (days > 0) return `${days} 天 ${hours} 小时`;
  if (hours > 0) return `${hours} 小时 ${minutes} 分`;
  return `${minutes} 分`;
}

/** 格式化百分比: 45.234 → "45.2%" */
export function formatPercent(value: number): string {
  return `${value.toFixed(1)}%`;
}

/** 格式化网速 (bytes/s): 1048576 → "1.0 MB/s" */
export function formatNetSpeed(bytesPerSec: number): string {
  return `${formatBytes(bytesPerSec)}/s`;
}

function trimTrailingZeros(value: string): string {
  return value.replace(/(?:\.0+|(\.\d+?)0+)$/, '$1');
}

export function bpsToMbpsInput(bytes?: number | null): string {
  if (!bytes || bytes <= 0) return '';

  const value = bytes / BYTES_PER_MEGABYTE;
  for (let decimals = 0; decimals <= 20; decimals += 1) {
    const formatted = trimTrailingZeros(value.toFixed(decimals));
    if (Math.round(Number.parseFloat(formatted) * BYTES_PER_MEGABYTE) === bytes) {
      return formatted;
    }
  }

  return trimTrailingZeros(value.toFixed(20));
}

/** 
 * 将输入框的 MB/s 字符串解析为 bytes/s (取整)，留空返回 0
 */
export function parseMbpsInputToBps(value: string): number | null {
  const trimmed = value.trim();
  if (trimmed === '') return 0;
  const parsed = Number.parseFloat(trimmed);
  if (!Number.isFinite(parsed) || parsed < 0) return null;
  return Math.round(parsed * BYTES_PER_MEGABYTE);
}

/** 将 Unix 时间戳转换为距今时长: 1609459200 → "5 年 73 天" */
export function formatInstallAge(unixTimestamp: number): string {
  if (!unixTimestamp || unixTimestamp <= 0) return '-';
  const seconds = Math.floor(Date.now() / 1000) - unixTimestamp;
  if (seconds < 0) return '-';
  return formatUptime(seconds);
}

export function formatTimestamp(value?: string): string {
  if (!value) return '-';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return '-';
  return date.toLocaleString();
}

export function describeFreshness(updatedAt?: string, freshUntil?: string): string {
  if (!updatedAt) return '时间未知';

  const updated = new Date(updatedAt);
  if (Number.isNaN(updated.getTime())) return '时间未知';

  if (freshUntil) {
    const expiry = new Date(freshUntil);
    if (!Number.isNaN(expiry.getTime()) && expiry.getTime() < Date.now()) {
      return '可能已过期';
    }
  }

  const seconds = Math.max(0, Math.floor((Date.now() - updated.getTime()) / 1000));
  if (seconds < 60) return `${seconds} 秒前更新`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)} 分钟前更新`;
  if (seconds < 86400) return `${Math.floor(seconds / 3600)} 小时前更新`;
  return `${Math.floor(seconds / 86400)} 天前更新`;
}
