import type { ButtonHTMLAttributes } from 'react';
import { cn } from '@/lib/utils';

type Tone = 'neutral' | 'primary' | 'success' | 'warning' | 'destructive';

interface TableActionIconButtonProps extends Omit<ButtonHTMLAttributes<HTMLButtonElement>, 'children'> {
  label: string;
  tone?: Tone;
  children: React.ReactNode;
}

const toneStyles: Record<Tone, string> = {
  neutral: 'text-muted-foreground hover:text-foreground hover:bg-muted/55',
  primary: 'text-brand-strong hover:text-brand-strong hover:bg-brand-soft',
  success: 'text-status-online-fg hover:text-status-online-fg hover:bg-status-online-soft',
  warning: 'text-status-warning-fg hover:text-status-warning-fg hover:bg-status-warning-soft',
  destructive: 'text-status-danger-fg hover:text-status-danger-fg hover:bg-status-danger-soft',
};

export function TableActionIconButton({
  label,
  tone = 'neutral',
  children,
  className,
  type = 'button',
  ...buttonProps
}: TableActionIconButtonProps) {
  return (
    <button
      type={type}
      className={cn(
        'inline-flex h-8 w-8 items-center justify-center rounded-md transition-colors',
        'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50 disabled:cursor-not-allowed disabled:opacity-50',
        toneStyles[tone],
        className,
      )}
      title={buttonProps.title ?? label}
      aria-label={buttonProps['aria-label'] ?? label}
      {...buttonProps}
    >
      {children}
    </button>
  );
}
