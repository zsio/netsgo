import i18n from 'i18next';
import LanguageDetector from 'i18next-browser-languagedetector';
import { initReactI18next } from 'react-i18next';
import { enUS } from './locales/en-US';
import { zhCN } from './locales/zh-CN';

export const SUPPORTED_LOCALES = ['en-US', 'zh-CN'] as const;
export type SupportedLocale = (typeof SUPPORTED_LOCALES)[number];

export const LOCALE_STORAGE_KEY = 'netsgo.locale';
export const DEFAULT_LOCALE: SupportedLocale = 'en-US';

export const resources = {
  'en-US': {
    translation: enUS,
  },
  'zh-CN': {
    translation: zhCN,
  },
} as const;

export function isSupportedLocale(value: string | null | undefined): value is SupportedLocale {
  return SUPPORTED_LOCALES.includes(value as SupportedLocale);
}

void i18n
  .use(LanguageDetector)
  .use(initReactI18next)
  .init({
    resources,
    supportedLngs: SUPPORTED_LOCALES,
    fallbackLng: DEFAULT_LOCALE,
    ns: ['translation'],
    defaultNS: 'translation',
    interpolation: {
      escapeValue: false,
    },
    initAsync: false,
    detection: {
      order: ['localStorage', 'navigator'],
      lookupLocalStorage: LOCALE_STORAGE_KEY,
      caches: ['localStorage'],
      convertDetectedLanguage: (language) => {
        if (!language) return DEFAULT_LOCALE;
        if (isSupportedLocale(language)) return language;
        const normalized = language.toLowerCase();
        if (normalized.startsWith('zh')) return 'zh-CN';
        if (normalized.startsWith('en')) return 'en-US';
        return DEFAULT_LOCALE;
      },
    },
    returnEmptyString: false,
  });

export { i18n };
