import { AlertTriangle, Bug, CircleAlert, Info } from 'lucide-react';
import type { LucideIcon } from 'lucide-react';

import type { ActivityCategory, ActivitySeverity } from '@/types';

export const allSeverities: ActivitySeverity[] = ['debug', 'info', 'warning', 'error'];

export interface ActivityFilterValue {
  severities: ActivitySeverity[];
  categories: ActivityCategory[];
  fromDate?: string;
  toDate?: string;
}

export const defaultActivityFilter: ActivityFilterValue = {
  severities: ['info', 'warning', 'error'],
  categories: [],
};

export const severityIcon: Record<ActivitySeverity, LucideIcon> = {
  debug: Bug,
  info: Info,
  warning: AlertTriangle,
  error: CircleAlert,
};

export const severityActiveClass: Record<ActivitySeverity, string> = {
  debug: 'border-border bg-muted/75 text-foreground/85',
  info: 'border-brand-soft-border bg-brand-soft text-brand-strong',
  warning: 'border-status-warning-border bg-status-warning-soft text-status-warning-fg',
  error: 'border-status-danger-border bg-status-danger-soft text-status-danger-fg',
};
