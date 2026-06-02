import { Languages } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { SUPPORTED_LOCALES, type SupportedLocale } from '@/i18n';
import { Button } from '@/components/ui/button';

const LANGUAGE_LABEL_KEYS: Record<SupportedLocale, string> = {
  'en-US': 'common.english',
  'zh-CN': 'common.chinese',
};

export function LanguageSwitcher() {
  const { t, i18n } = useTranslation();
  const currentLanguage = SUPPORTED_LOCALES.includes(i18n.resolvedLanguage as SupportedLocale)
    ? i18n.resolvedLanguage as SupportedLocale
    : 'en-US';

  const nextLanguage: SupportedLocale = currentLanguage === 'en-US' ? 'zh-CN' : 'en-US';

  return (
    <Button
      type="button"
      variant="ghost"
      size="sm"
      className="h-8 w-full justify-start gap-2 px-2 text-muted-foreground hover:text-foreground"
      onClick={() => void i18n.changeLanguage(nextLanguage)}
      title={t('common.language')}
      aria-label={t('common.language')}
    >
      <Languages className="h-4 w-4" />
      <span>{t('common.language')}</span>
      <span className="ml-auto text-xs">{t(LANGUAGE_LABEL_KEYS[currentLanguage])}</span>
    </Button>
  );
}
