import { i18n } from '@/i18n';
import type { TunnelIssue } from '@/types';

type TunnelIssueMessageInput = Pick<TunnelIssue, 'code' | 'scope' | 'message'>;

function getTunnelIssueLocaleKey(issue: TunnelIssueMessageInput) {
  if (issue.code === 'capability_not_supported') {
    if (issue.scope === 'target_client') {
      return 'issues.capability_not_supported_target_client';
    }
    if (issue.scope === 'ingress_client') {
      return 'issues.capability_not_supported_ingress_client';
    }
  }
  return `issues.${issue.code}`;
}

export function formatTunnelIssueMessage(issue: TunnelIssueMessageInput) {
  const key = getTunnelIssueLocaleKey(issue);
  if (i18n.exists(key)) {
    const translated = i18n.t(key, { message: issue.message, defaultValue: issue.message });
    if (translated) {
      return translated;
    }
  }
  return issue.message || issue.code;
}

export function formatTunnelIssueTooltipLine(issue: Pick<TunnelIssue, 'code' | 'scope' | 'message' | 'severity'>) {
  return `${issue.severity}: ${formatTunnelIssueMessage(issue)}`;
}
