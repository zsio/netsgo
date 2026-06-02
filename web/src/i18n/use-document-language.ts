import { useEffect } from 'react';
import { useTranslation } from 'react-i18next';
import { DEFAULT_LOCALE, isSupportedLocale } from '@/i18n';

export function useDocumentLanguage() {
  const { i18n } = useTranslation();

  useEffect(() => {
    const applyLanguage = (language: string) => {
      document.documentElement.lang = isSupportedLocale(language) ? language : DEFAULT_LOCALE;
    };

    applyLanguage(i18n.resolvedLanguage || i18n.language);
    i18n.on('languageChanged', applyLanguage);

    return () => {
      i18n.off('languageChanged', applyLanguage);
    };
  }, [i18n]);
}
