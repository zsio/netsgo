import type { TFunction } from 'i18next';

import type { ActivityItem as ActivityItemType } from '@/types';

export function readableActivityClientName(item: ActivityItemType, clientId: string, t: TFunction) {
  const subject = item.clients.find((candidate) => candidate.client_id === clientId);
  return subject?.display_name?.trim() || subject?.hostname?.trim() || t('activity.unknownClient');
}

export function readableActivityTunnelName(item: ActivityItemType, tunnelId: string, t: TFunction) {
  const subject = item.tunnels.find((candidate) => candidate.tunnel_id === tunnelId);
  return subject?.name?.trim() || t('activity.unknownTunnel');
}

export function formatActivityItemSummary(item: ActivityItemType, t: TFunction) {
  if (item.payload_version !== 1 || !item.payload.summary_key) return t('activity.unknownSummary');
  const args = { ...item.payload.summary_args };
  const client = item.clients.find((subject) => subject.relation === 'subject');
  const tunnel = item.tunnels.find((subject) => subject.relation === 'subject');
  if (client) args.client_name = readableActivityClientName(item, client.client_id, t);
  if (tunnel) args.tunnel_name = readableActivityTunnelName(item, tunnel.tunnel_id, t);
  return t(item.payload.summary_key, {
    ...args,
    defaultValue: t('activity.unknownSummary'),
  });
}

const offlineCauseCode: Record<string, true> = {
  normal_closure: true,
  server_shutdown: true,
  timeout: true,
  user_disabled: true,
  replaced: true,
};

export function formatActivityItemReason(item: ActivityItemType, t: TFunction) {
  const code = item.payload.reason_code;
  if (!code || code === 'unknown') return '';
  if (item.category === 'client' && item.action === 'offline' && !offlineCauseCode[code]) return '';
  return t(`activity.reason.${code}`, { defaultValue: '' });
}

export function formatActivityCompactMeta(item: ActivityItemType, summary: string, t: TFunction): string[] {
  const parts: string[] = [];
  const reason = formatActivityItemReason(item, t);
  if (reason) {
    parts.push(reason);
  }

  for (const subject of item.clients) {
    const name = subject.display_name?.trim() || subject.hostname?.trim();
    if (name && !summary.includes(name)) {
      parts.push(name);
      break;
    }
  }

  if (item.actor.type !== 'system' && item.actor.type !== 'unknown') {
    const typeLabel = t(`activity.actor.${item.actor.type}`);
    const rawName = item.actor.name?.trim();
    const actorName = rawName && rawName !== item.actor.id ? rawName : '';
    const echoesCategory = item.actor.type === item.category;
    const actorText =
      !actorName || summary.includes(actorName)
        ? echoesCategory
          ? ''
          : typeLabel
        : echoesCategory || !typeLabel
          ? actorName
          : `${typeLabel} ${actorName}`;
    if (actorText && !parts.includes(actorText)) {
      parts.push(actorText);
    }
  }

  return parts;
}
