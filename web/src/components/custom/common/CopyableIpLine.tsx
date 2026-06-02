import type { ReactNode } from 'react';
import { useTranslation } from 'react-i18next';

import { CopyButton } from './CopyButton';

interface CopyableIpLineProps {
  icon: ReactNode;
  value: string;
  title: string;
  primary?: boolean;
}

export function CopyableIpLine({ icon, value, title, primary = false }: CopyableIpLineProps) {
  const { t } = useTranslation();

  return (
    <div className={primary ? 'font-medium text-sm text-foreground min-w-0' : 'text-xs text-muted-foreground min-w-0'}>
      <span className="inline-flex items-center gap-1.5 min-w-0 group/ip">
        <span className="shrink-0 text-muted-foreground" title={title} aria-label={title}>{icon}</span>
        <span className="font-mono break-all">{value}</span>
        <CopyButton
          value={value}
          title={t('clients.copyLabel', { label: title })}
          className="opacity-0 group-hover/ip:opacity-100 focus-visible:opacity-100"
        />
      </span>
    </div>
  );
}
