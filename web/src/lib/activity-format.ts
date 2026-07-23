import { i18n, DEFAULT_LOCALE } from '@/i18n';
import { formatRelativeTimestamp } from '@/lib/format';

function locale() {
  return i18n.resolvedLanguage || i18n.language || DEFAULT_LOCALE;
}

export function formatActivityRelativeTime(value: string, now = Date.now()) {
  return formatRelativeTimestamp(value, now);
}

export function formatActivityAbsoluteTime(value: string) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return i18n.t('format.unknownTime');
  return new Intl.DateTimeFormat(locale(), { dateStyle: 'full', timeStyle: 'long' }).format(date);
}
