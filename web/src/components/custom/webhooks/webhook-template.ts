import type {
  ActivityWebhookConfig,
  WebhookCatalog,
  WebhookEventKey,
  WebhookTemplateSurface,
  WebhookTemplateValue,
  WebhookVariable,
} from '@/types/webhook';

const TOKEN_PATTERN = /{{\s*([^}]+?)\s*}}/g;
const EXACT_TOKEN_PATTERN = /^{{\s*([^}]+?)\s*}}$/;
const HEADER_NAME_PATTERN = /^[!#$%&'*+.^_`|~0-9A-Za-z-]+$/;
const RESTRICTED_HEADERS = new Set([
  'connection',
  'content-length',
  'host',
  'transfer-encoding',
  'user-agent',
  'x-netsgo-attempt',
  'x-netsgo-delivery',
  'x-netsgo-event',
]);

export interface WebhookTemplateIssue {
  code: 'unknownVariable' | 'unavailableVariable' | 'unsupportedSurface';
  key: string;
  from: number;
  to: number;
}

export interface WebhookValidationIssue {
  field: 'name' | 'events' | 'targets' | 'url' | 'headers' | 'body' | 'minIntervalSeconds';
  code:
    | 'required'
    | 'invalidInterval'
    | 'invalidUrl'
    | 'invalidUrlScheme'
    | 'invalidJson'
    | 'bodyMustBeObject'
    | 'invalidHeader'
    | 'duplicateHeader'
    | 'restrictedHeader'
    | WebhookTemplateIssue['code'];
  key?: string;
}

export interface RenderedWebhookRequest {
  url: string;
  headers: Array<{ key: string; value: string }>;
  body: string;
  bodyError: boolean;
  values: Record<string, WebhookTemplateValue>;
}

function webhookVariableSupportsEvents(variable: WebhookVariable, events: WebhookEventKey[]) {
  if (variable.available_for_events === 'all') return true;
  return events.length > 0 && events.every((event) => variable.available_for_events.includes(event));
}

export function getWebhookVariables(
  catalog: WebhookCatalog,
  events: WebhookEventKey[],
  surface: WebhookTemplateSurface,
) {
  return catalog.variables.filter((variable) => (
    variable.surfaces.includes(surface)
    && webhookVariableSupportsEvents(variable, events)
  ));
}

// A picker entry is one visible row: language variants of the same variable
// (event.name.zh-CN / event.name.en-US) collapse into a single baseKey row,
// resolved to the picker's currently selected language.
export interface PickerVariable {
  baseKey: string;
  variable: WebhookVariable;
}

function localeSuffix(catalog: WebhookCatalog, key: string) {
  return (catalog.locales ?? []).find(
    (locale) => key.length > locale.length + 1 && key.endsWith(`.${locale}`),
  );
}

export function getPickerVariables(
  catalog: WebhookCatalog,
  events: WebhookEventKey[],
  surface: WebhookTemplateSurface,
  locale: string,
): PickerVariable[] {
  const order: string[] = [];
  const seen = new Set<string>();
  const chosen = new Map<string, WebhookVariable>();
  for (const variable of getWebhookVariables(catalog, events, surface)) {
    const suffix = localeSuffix(catalog, variable.key);
    const baseKey = suffix ? variable.key.slice(0, variable.key.length - suffix.length - 1) : variable.key;
    if (!seen.has(baseKey)) {
      seen.add(baseKey);
      order.push(baseKey);
    }
    const existing = chosen.get(baseKey);
    if (!existing || (!existing.key.endsWith(`.${locale}`) && variable.key.endsWith(`.${locale}`))) {
      chosen.set(baseKey, variable);
    }
  }
  return order.map((baseKey) => ({ baseKey, variable: chosen.get(baseKey)! }));
}

export function webhookVariableSample(
  catalog: WebhookCatalog,
  variable: WebhookVariable,
  event: WebhookEventKey,
  webhook?: Pick<ActivityWebhookConfig, 'name'>,
) {
  if (variable.key === 'webhook.name' && webhook) return webhook.name || 'Webhook';
  const value = catalog.fixtures[event]?.[variable.key];
  return typeof value === 'string' ? value : JSON.stringify(value ?? null);
}

export function getTemplateIssues(
  value: string,
  events: WebhookEventKey[],
  surface: WebhookTemplateSurface,
  variables: WebhookVariable[],
) {
  const issues: WebhookTemplateIssue[] = [];
  for (const match of value.matchAll(TOKEN_PATTERN)) {
    const key = match[1].trim();
    const variable = variables.find((entry) => entry.key === key);
    const from = match.index ?? 0;
    const to = from + match[0].length;
    if (!variable) {
      issues.push({ code: 'unknownVariable', key, from, to });
    } else if (!variable.surfaces.includes(surface)) {
      issues.push({ code: 'unsupportedSurface', key, from, to });
    } else if (!webhookVariableSupportsEvents(variable, events)) {
      issues.push({ code: 'unavailableVariable', key, from, to });
    }
  }
  return issues;
}

function templateValueToText(value: WebhookTemplateValue | undefined) {
  if (value === undefined) return undefined;
  if (typeof value === 'string') return value;
  if (value === null) return '';
  return typeof value === 'object' ? JSON.stringify(value) : String(value);
}

export function renderTemplate(
  value: string,
  values: Record<string, WebhookTemplateValue>,
  encode = false,
) {
  return value.replace(TOKEN_PATTERN, (token, rawKey: string) => {
    const replacement = templateValueToText(values[rawKey.trim()]);
    if (replacement === undefined) return token;
    return encode ? encodeURIComponent(replacement) : replacement;
  });
}

export function renderJsonBody(body: string, values: Record<string, WebhookTemplateValue>) {
  const parsed: unknown = JSON.parse(body);
  const visit = (value: unknown): unknown => {
    if (typeof value === 'string') {
      const exact = value.match(EXACT_TOKEN_PATTERN);
      if (exact) {
        const replacement = values[exact[1].trim()];
        if (replacement !== undefined) return replacement;
      }
      return renderTemplate(value, values);
    }
    if (Array.isArray(value)) return value.map(visit);
    if (value && typeof value === 'object') {
      return Object.fromEntries(Object.entries(value).map(([key, entry]) => [key, visit(entry)]));
    }
    return value;
  };
  return JSON.stringify(visit(parsed), null, 2);
}

export function renderWebhookRequest(
  webhook: ActivityWebhookConfig,
  event: WebhookEventKey,
  catalog: WebhookCatalog,
): RenderedWebhookRequest {
  const fixture = catalog.fixtures[event] ?? {};
  const values = {
    ...fixture,
    'webhook.name': webhook.name || 'Webhook',
  };
  let body = '';
  let bodyError = false;
  if (webhook.method === 'POST') {
    try {
      body = renderJsonBody(webhook.body, values);
    } catch {
      body = webhook.body;
      bodyError = true;
    }
  }
  return {
    url: renderTemplate(webhook.url, values, true),
    headers: webhook.headers
      .filter((header) => header.key.trim())
      .map((header) => ({
        key: header.key.trim(),
        value: renderTemplate(header.value, values),
      })),
    body,
    bodyError,
    values,
  };
}

export function validateWebhook(webhook: ActivityWebhookConfig, catalog: WebhookCatalog): WebhookValidationIssue[] {
  const issues: WebhookValidationIssue[] = [];
  if (!webhook.name.trim()) issues.push({ field: 'name', code: 'required' });
  if (!Number.isInteger(webhook.minIntervalSeconds) || webhook.minIntervalSeconds < 0 || webhook.minIntervalSeconds > 86400) {
    issues.push({ field: 'minIntervalSeconds', code: 'invalidInterval' });
  }
  if (webhook.events.length === 0) issues.push({ field: 'events', code: 'required' });
  if (webhook.targetMode === 'selected' && webhook.targetIds.length === 0) {
    issues.push({ field: 'targets', code: 'required' });
  }

  if (!webhook.url.trim()) {
    issues.push({ field: 'url', code: 'required' });
  } else {
    for (const issue of getTemplateIssues(webhook.url, webhook.events, 'url', catalog.variables)) {
      issues.push({ field: 'url', code: issue.code, key: issue.key });
    }
    try {
      const event = webhook.events[0] ?? (webhook.targetKind === 'client' ? 'client.online' : 'tunnel.runtime_changed');
      const rendered = renderWebhookRequest(webhook, event, catalog).url;
      const parsed = new URL(rendered);
      if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') {
        issues.push({ field: 'url', code: 'invalidUrlScheme' });
      }
    } catch {
      issues.push({ field: 'url', code: 'invalidUrl' });
    }
  }

  const seenHeaders = new Set<string>();
  for (const header of webhook.headers) {
    if (!header.key.trim() && !header.value.trim()) continue;
    const normalizedKey = header.key.trim().toLowerCase();
    if (!HEADER_NAME_PATTERN.test(header.key.trim()) || /[\r\n]/.test(header.value)) {
      issues.push({ field: 'headers', code: 'invalidHeader', key: header.key || undefined });
      continue;
    }
    if (seenHeaders.has(normalizedKey)) issues.push({ field: 'headers', code: 'duplicateHeader', key: header.key });
    seenHeaders.add(normalizedKey);
    if (RESTRICTED_HEADERS.has(normalizedKey)) issues.push({ field: 'headers', code: 'restrictedHeader', key: header.key });
    for (const issue of getTemplateIssues(header.value, webhook.events, 'header', catalog.variables)) {
      issues.push({ field: 'headers', code: issue.code, key: issue.key });
    }
  }

  if (webhook.method === 'POST') {
    try {
      const parsed: unknown = JSON.parse(webhook.body);
      if (!parsed || Array.isArray(parsed) || typeof parsed !== 'object') {
        issues.push({ field: 'body', code: 'bodyMustBeObject' });
      }
    } catch {
      issues.push({ field: 'body', code: 'invalidJson' });
    }
    for (const issue of getTemplateIssues(webhook.body, webhook.events, 'body', catalog.variables)) {
      issues.push({ field: 'body', code: issue.code, key: issue.key });
    }
  }
  return issues;
}
