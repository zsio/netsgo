import type { ButtonHTMLAttributes } from 'react';
import { cn } from '@/lib/utils';

type Tone = 'neutral' | 'primary' | 'success' | 'warning' | 'destructive';

interface TableActionIconButtonProps extends Omit<ButtonHTMLAttributes<HTMLButtonElement>, 'children'> {
  label: string;
  tone?: Tone;
  children: React.ReactNode;
}

const toneStyles: Record<Tone, string> = {
  neutral: 'text-muted-foreground hover:text-foreground hover:bg-muted/45',
  primary: 'text-primary hover:text-primary hover:bg-primary/10',
  success: 'text-emerald-500 hover:text-emerald-600 hover:bg-emerald-500/10',
  warning: 'text-amber-500 hover:text-amber-600 hover:bg-amber-500/10',
  destructive: 'text-destructive hover:text-destructive hover:bg-destructive/10',
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
