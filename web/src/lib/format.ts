import { i18n, DEFAULT_LOCALE } from '@/i18n';

const UNITS = ["B", "KB", "MB", "GB", "TB"] as const;
const BYTES_PER_MEBIBYTE = 1024 * 1024;
const NET_SPEED_UNITS = ["B", "KiB", "MiB", "GiB", "TiB"] as const;

export function formatBytes(bytes: number): string {
  if (bytes === 0) return "0 B";
  const i = Math.floor(Math.log(bytes) / Math.log(1024));
  const idx = Math.min(i, UNITS.length - 1);
  return `${(bytes / Math.pow(1024, idx)).toFixed(1)} ${UNITS[idx]}`;
}

function currentLocale() {
  return i18n.resolvedLanguage || i18n.language || DEFAULT_LOCALE;
}

function formatDurationPart(value: number, unit: 'year' | 'day' | 'hour' | 'minute' | 'second') {
  if (currentLocale().startsWith('zh')) {
    return `${value} ${i18n.t(`format.${unit}`, { defaultValue: unit })}`;
  }
  const key = value === 1 ? unit : `${unit}_other`;
  return `${value} ${i18n.t(`format.${key}`, { defaultValue: unit })}`;
}

export function formatUptime(seconds: number): string {
  if (seconds < 60) return formatDurationPart(seconds, 'second');

  const years = Math.floor(seconds / (86400 * 365));
  const days = Math.floor((seconds % (86400 * 365)) / 86400);
  const hours = Math.floor((seconds % 86400) / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);

  if (years > 0) return `${formatDurationPart(years, 'year')} ${formatDurationPart(days, 'day')}`;
  if (days > 0) return `${formatDurationPart(days, 'day')} ${formatDurationPart(hours, 'hour')}`;
  if (hours > 0) return `${formatDurationPart(hours, 'hour')} ${formatDurationPart(minutes, 'minute')}`;
  return formatDurationPart(minutes, 'minute');
}

export function formatPercent(value: number): string {
  return new Intl.NumberFormat(currentLocale(), {
    maximumFractionDigits: 1,
    minimumFractionDigits: 1,
  }).format(value) + '%';
}

export function formatNetSpeed(bytesPerSec: number): string {
  if (bytesPerSec === 0) return '0 B/s';
  const i = Math.floor(Math.log(bytesPerSec) / Math.log(1024));
  const idx = Math.min(i, NET_SPEED_UNITS.length - 1);
  return `${(bytesPerSec / Math.pow(1024, idx)).toFixed(1)} ${NET_SPEED_UNITS[idx]}/s`;
}

export function formatBandwidthLimit(bytesPerSec?: number | null): string {
  if (!bytesPerSec || bytesPerSec <= 0) return '∞';
  return formatNetSpeed(bytesPerSec);
}

function trimTrailingZeros(value: string): string {
  return value.replace(/(?:\.0+|(\.\d+?)0+)$/, '$1');
}

export function bpsToMbpsInput(bytes?: number | null): string {
  if (!bytes || bytes <= 0) return '';

  const value = bytes / BYTES_PER_MEBIBYTE;
  for (let decimals = 0; decimals <= 20; decimals += 1) {
    const formatted = trimTrailingZeros(value.toFixed(decimals));
    if (Math.round(Number.parseFloat(formatted) * BYTES_PER_MEBIBYTE) === bytes) {
      return formatted;
    }
  }

  return trimTrailingZeros(value.toFixed(20));
}

export function parseMbpsInputToBps(value: string): number | null {
  const trimmed = value.trim();
  if (trimmed === '') return 0;
  const parsed = Number.parseFloat(trimmed);
  if (!Number.isFinite(parsed) || parsed < 0) return null;
  return Math.round(parsed * BYTES_PER_MEBIBYTE);
}

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
  return new Intl.DateTimeFormat(currentLocale(), {
    dateStyle: 'medium',
    timeStyle: 'medium',
  }).format(date);
}

export function describeFreshness(updatedAt?: string, freshUntil?: string): string {
  if (!updatedAt) return i18n.t('format.unknownTime');

  const updated = new Date(updatedAt);
  if (Number.isNaN(updated.getTime())) return i18n.t('format.unknownTime');

  if (freshUntil) {
    const expiry = new Date(freshUntil);
    if (!Number.isNaN(expiry.getTime()) && expiry.getTime() < Date.now()) {
      return i18n.t('format.stale');
    }
  }

  const seconds = Math.max(0, Math.floor((Date.now() - updated.getTime()) / 1000));
  const formatter = new Intl.RelativeTimeFormat(currentLocale(), { numeric: 'auto' });
  if (seconds < 60) return formatter.format(-seconds, 'second');
  if (seconds < 3600) return formatter.format(-Math.floor(seconds / 60), 'minute');
  if (seconds < 86400) return formatter.format(-Math.floor(seconds / 3600), 'hour');
  return formatter.format(-Math.floor(seconds / 86400), 'day');
}
