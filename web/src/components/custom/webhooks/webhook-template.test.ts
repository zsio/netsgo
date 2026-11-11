import { describe, expect, test } from 'vitest';

import type { ActivityWebhookConfig, WebhookCatalog, WebhookVariable } from '@/types/webhook';
import { activityWebhookFromAPI, activityWebhookToAPI, createEmptyWebhook } from '@/types/webhook';
import {
  getPickerVariables,
  getTemplateIssues,
  getWebhookVariables,
  renderJsonBody,
  renderTemplate,
  renderWebhookRequest,
  validateWebhook,
  webhookVariableSample,
} from './webhook-template';

const defaultBody = `{
  "delivery": { "attempt": "{{delivery.attempt}}" },
  "event": {
    "summary": { "zh-CN": "{{event.summary.zh-CN}}", "en-US": "{{event.summary.en-US}}" }
  },
  "webhook": { "name": "{{webhook.name}}" }
}`;

const allEvents = 'all' as const;
const body = ['body'] as const;
const everySurface = ['url', 'header', 'body'] as const;
const variables: WebhookVariable[] = [
  { key: 'delivery.id', group: 'delivery', value_type: 'text', surfaces: [...everySurface], available_for_events: allEvents },
  { key: 'delivery.attempt', group: 'delivery', value_type: 'number', surfaces: [...body], available_for_events: allEvents },
  { key: 'event.name.zh-CN', group: 'event', value_type: 'text', surfaces: [...everySurface], available_for_events: allEvents },
  { key: 'event.name.en-US', group: 'event', value_type: 'text', surfaces: [...everySurface], available_for_events: allEvents },
  { key: 'event.summary.zh-CN', group: 'event', value_type: 'text', surfaces: [...everySurface], available_for_events: allEvents },
  { key: 'event.summary.en-US', group: 'event', value_type: 'text', surfaces: [...everySurface], available_for_events: allEvents },
  { key: 'client.id', group: 'client', value_type: 'text', surfaces: [...everySurface], available_for_events: ['client.online', 'tunnel.runtime_error'] },
  { key: 'client.name', group: 'client', value_type: 'text', surfaces: [...everySurface], available_for_events: ['client.online', 'tunnel.runtime_error'] },
  { key: 'tunnel.id', group: 'tunnel', value_type: 'text', surfaces: [...everySurface], available_for_events: ['tunnel.runtime_error'] },
  { key: 'webhook.name', group: 'webhook', value_type: 'text', surfaces: [...everySurface], available_for_events: allEvents },
];

const fixtures = {
  'client.online': {
    'delivery.id': 'dlv-client-online',
    'delivery.attempt': 1,
    'event.name.zh-CN': '客户端上线',
    'event.name.en-US': 'Client online',
    'event.summary.zh-CN': 'client-a 已上线',
    'event.summary.en-US': 'client-a came online',
    'client.id': 'client-a',
    'client.name': 'Primary client',
  },
  'p2p.connected': {
    'delivery.id': 'dlv-p2p-connected',
    'delivery.attempt': 1,
    'event.name.zh-CN': 'P2P 已直连',
    'event.name.en-US': 'P2P connected',
    'event.summary.zh-CN': 'P2P 已直连',
    'event.summary.en-US': 'P2P connected',
  },
  'p2p.failed': {
    'delivery.id': 'dlv-p2p-failed',
    'delivery.attempt': 1,
    'event.name.zh-CN': 'P2P 直连失败',
    'event.name.en-US': 'P2P failed',
    'event.summary.zh-CN': 'P2P 直连失败',
    'event.summary.en-US': 'P2P failed',
  },
  'tunnel.runtime_error': {
    'delivery.id': 'dlv-tunnel-error',
    'delivery.attempt': 1,
    'event.name.zh-CN': '隧道运行异常',
    'event.name.en-US': 'Tunnel runtime error',
    'event.summary.zh-CN': 'tunnel-a 运行异常',
    'event.summary.en-US': 'tunnel-a encountered a runtime error',
    'client.id': 'client-owner',
    'client.name': 'Renamed owner',
    'tunnel.id': 'tunnel-a',
  },
} as WebhookCatalog['fixtures'];

const catalog: WebhookCatalog = {
  events: [
    { key: 'client.online', target_kind: 'client', family: 'client' },
    { key: 'tunnel.runtime_error', target_kind: 'tunnel', family: 'tunnel' },
    { key: 'p2p.connected', target_kind: 'tunnel', family: 'p2p' },
    { key: 'p2p.failed', target_kind: 'tunnel', family: 'p2p' },
  ],
  variables,
  fixtures,
  default_body: defaultBody,
  locales: ['en-US', 'zh-CN'],
};

function webhook(overrides: Partial<ActivityWebhookConfig>): ActivityWebhookConfig {
  return {
    id: 'wh_test',
    revision: 0,
    name: 'Test webhook',
    enabled: false,
    targetKind: 'client',
    targetMode: 'all',
    targetIds: [],
    method: 'POST',
    url: 'https://hooks.example.test/events',
    headers: [],
    body: defaultBody,
    events: ['client.online'],
    minIntervalSeconds: 0,
    calls24h: 0,
    lastStatus: 'idle',
    consecutiveFailures: 0,
    lastCalledAt: null,
    createdAt: '2026-08-25T00:00:00Z',
    updatedAt: '2026-08-25T00:00:00Z',
    ...overrides,
  };
}

describe('webhook request templates', () => {
  test('renders one P2P event with typed client, tunnel, and match arrays', () => {
    const request = renderWebhookRequest(webhook({
      targetKind: 'tunnel',
      events: ['p2p.connected'],
    }), 'p2p.connected', catalog);

    const rendered = JSON.parse(request.body);
    expect(rendered.delivery.attempt).toBe(1);
    expect(rendered.event.summary['zh-CN']).toBe('P2P 已直连');
    expect(rendered.webhook.name).toBe('Test webhook');
  });

  test('rejects a singular tunnel variable when P2P events share the template', () => {
    const issues = validateWebhook(webhook({
      targetKind: 'tunnel',
      events: ['tunnel.runtime_error', 'p2p.failed'],
      body: '{"tunnel_id":"{{tunnel.id}}"}',
    }), catalog);

    expect(issues).toContainEqual({
      field: 'body',
      code: 'unavailableVariable',
      key: 'tunnel.id',
    });
  });

  test('uses catalog fixtures as the single source of preview examples', () => {
    expect(catalog.fixtures['client.online']['client.id']).toBe('client-a');
    expect(catalog.fixtures['client.online']['event.name.en-US']).toBe('Client online');
    expect(catalog.fixtures['client.online']['event.name.zh-CN']).toBe('客户端上线');
    expect(catalog.fixtures['p2p.connected']['event.name.en-US']).toBe('P2P connected');
  });

  test('renders each language variable in its own language', () => {
    const request = renderWebhookRequest(webhook({
      body: '{"zh-CN":"{{event.name.zh-CN}}","en-US":"{{event.name.en-US}}"}',
    }), 'client.online', catalog);

    expect(JSON.parse(request.body)).toEqual({ 'zh-CN': '客户端上线', 'en-US': 'Client online' });
  });

  test('renders Header values in plaintext', () => {
    const request = renderWebhookRequest(webhook({
      headers: [{ id: 'authorization', key: 'Authorization', value: 'Bearer demo-client-token' }],
    }), 'client.online', catalog);

    expect(request.headers).toEqual([{ key: 'Authorization', value: 'Bearer demo-client-token' }]);
  });

  test('URL-encodes substitutions while retaining unknown tokens for diagnosis', () => {
    const request = renderWebhookRequest(webhook({
      name: '深圳 / primary',
      url: 'https://hooks.example.test/{{webhook.name}}?client={{client.id}}&missing={{missing.value}}',
    }), 'client.online', catalog);

    expect(request.url).toBe('https://hooks.example.test/%E6%B7%B1%E5%9C%B3%20%2F%20primary?client=client-a&missing={{missing.value}}');
  });

  test('preserves exact-token JSON types through nested objects and arrays', () => {
    const rendered = renderJsonBody(`{
      "attempt": "{{delivery.attempt}}",
      "name": "{{event.name.en-US}}",
      "items": ["{{client.id}}", "attempt={{delivery.attempt}}"],
      "missing": "{{missing.value}}"
    }`, catalog.fixtures['client.online']);

    expect(JSON.parse(rendered)).toEqual({
      attempt: 1,
      name: 'Client online',
      items: ['client-a', 'attempt=1'],
      missing: '{{missing.value}}',
    });
  });

  test('reports preview body errors without replacing the editable source', () => {
    const request = renderWebhookRequest(webhook({ body: '{invalid' }), 'client.online', catalog);
    expect(request.bodyError).toBe(true);
    expect(request.body).toBe('{invalid');
  });

  test('GET previews never render a body', () => {
    const request = renderWebhookRequest(webhook({ method: 'GET', body: '{invalid' }), 'client.online', catalog);
    expect(request.bodyError).toBe(false);
    expect(request.body).toBe('');
  });

  test('filters picker variables by surface and every selected event', () => {
    expect(getWebhookVariables(catalog, ['client.online'], 'url').map((item) => item.key)).toEqual([
      'delivery.id',
      'event.name.zh-CN',
      'event.name.en-US',
      'event.summary.zh-CN',
      'event.summary.en-US',
      'client.id',
      'client.name',
      'webhook.name',
    ]);
    expect(getWebhookVariables(catalog, ['tunnel.runtime_error'], 'url').map((item) => item.key)).toEqual([
      'delivery.id',
      'event.name.zh-CN',
      'event.name.en-US',
      'event.summary.zh-CN',
      'event.summary.en-US',
      'client.id',
      'client.name',
      'tunnel.id',
      'webhook.name',
    ]);
    expect(getWebhookVariables(catalog, ['tunnel.runtime_error', 'p2p.failed'], 'url').map((item) => item.key)).toEqual([
      'delivery.id',
      'event.name.zh-CN',
      'event.name.en-US',
      'event.summary.zh-CN',
      'event.summary.en-US',
      'webhook.name',
    ]);
  });

  test('formats fixture and current-webhook samples for the variable picker', () => {
    expect(webhookVariableSample(catalog, variables.find((item) => item.key === 'client.name')!, 'client.online')).toBe('Primary client');
    expect(webhookVariableSample(catalog, variables.find((item) => item.key === 'client.name')!, 'tunnel.runtime_error')).toBe('Renamed owner');
    expect(webhookVariableSample(catalog, variables.find((item) => item.key === 'webhook.name')!, 'client.online', webhook({ name: 'Current name' }))).toBe('Current name');
    expect(webhookVariableSample(catalog, variables.find((item) => item.key === 'webhook.name')!, 'client.online', webhook({ name: '' }))).toBe('Webhook');
  });

  test('collapses language variants into one picker row for the selected language', () => {
    const zh = getPickerVariables(catalog, ['client.online'], 'url', 'zh-CN');
    expect(zh.map((entry) => entry.baseKey)).toContain('event.name');
    const zhName = zh.find((entry) => entry.baseKey === 'event.name')!;
    expect(zhName.variable.key).toBe('event.name.zh-CN');
    expect(webhookVariableSample(catalog, zhName.variable, 'client.online')).toBe('客户端上线');
    expect(zh.filter((entry) => entry.baseKey === 'event.name')).toHaveLength(1);

    const en = getPickerVariables(catalog, ['client.online'], 'url', 'en-US');
    expect(en.find((entry) => entry.baseKey === 'event.name')!.variable.key).toBe('event.name.en-US');

    // non-localized variables pass through unchanged
    expect(zh.find((entry) => entry.baseKey === 'client.id')!.variable.key).toBe('client.id');
  });

  test('renders plain text values and URL encoding directly', () => {
    const values = { text: 'a b/深圳', object: { ok: true }, nil: null };
    expect(renderTemplate('{{text}} {{object}} {{nil}} {{missing}}', values)).toBe('a b/深圳 {"ok":true}  {{missing}}');
    expect(renderTemplate('{{text}}', values, true)).toBe('a%20b%2F%E6%B7%B1%E5%9C%B3');
  });
});

describe('webhook configuration validation', () => {
  test('reports every missing required field in one pass', () => {
    expect(validateWebhook(webhook({
      name: ' ',
      events: [],
      targetMode: 'selected',
      targetIds: [],
      url: ' ',
    }), catalog)).toEqual(expect.arrayContaining([
      { field: 'name', code: 'required' },
      { field: 'events', code: 'required' },
      { field: 'targets', code: 'required' },
      { field: 'url', code: 'required' },
    ]));
  });

  test.each([
    ['not a URL', 'invalidUrl'],
    ['ftp://hooks.example.test/path', 'invalidUrlScheme'],
  ])('rejects destination %s with %s', (url, code) => {
    expect(validateWebhook(webhook({ url }), catalog)).toContainEqual({ field: 'url', code });
  });

  test('reports unknown, unsupported-surface, and unavailable URL variables', () => {
    expect(getTemplateIssues('{{missing.value}}', ['client.online'], 'url', variables)).toEqual([
      expect.objectContaining({ code: 'unknownVariable', key: 'missing.value' }),
    ]);
    expect(validateWebhook(webhook({ url: 'https://example.test/{{delivery.attempt}}' }), catalog)).toContainEqual({
      field: 'url', code: 'unsupportedSurface', key: 'delivery.attempt',
    });
    expect(validateWebhook(webhook({ url: 'https://example.test/{{tunnel.id}}' }), catalog)).toContainEqual({
      field: 'url', code: 'unavailableVariable', key: 'tunnel.id',
    });
    for (const key of ['event.id', 'event.type', 'webhook.id', 'client.hostname']) {
      expect(getTemplateIssues(`{{${key}}}`, ['client.online'], 'url', variables)).toEqual([
        expect.objectContaining({ code: 'unknownVariable', key }),
      ]);
    }
  });

  test('rejects malformed, duplicated, restricted, and injected Headers', () => {
    const issues = validateWebhook(webhook({
      headers: [
        { id: 'invalid', key: 'Bad Header', value: 'value' },
        { id: 'first', key: 'X-Test', value: 'one' },
        { id: 'duplicate', key: 'x-test', value: 'two' },
        { id: 'restricted', key: 'X-NetsGo-Attempt', value: '99' },
        { id: 'newline', key: 'X-Newline', value: 'one\ntwo' },
      ],
    }), catalog);

    expect(issues).toEqual(expect.arrayContaining([
      { field: 'headers', code: 'invalidHeader', key: 'Bad Header' },
      { field: 'headers', code: 'duplicateHeader', key: 'x-test' },
      { field: 'headers', code: 'restrictedHeader', key: 'X-NetsGo-Attempt' },
      { field: 'headers', code: 'invalidHeader', key: 'X-Newline' },
    ]));
  });

  test.each([
    ['{invalid', 'invalidJson'],
    ['null', 'bodyMustBeObject'],
    ['[]', 'bodyMustBeObject'],
    ['"text"', 'bodyMustBeObject'],
  ])('rejects POST body %s with %s', (bodyValue, code) => {
    expect(validateWebhook(webhook({ body: bodyValue }), catalog)).toContainEqual({ field: 'body', code });
  });

  test('validates body variables across all selected events', () => {
    expect(validateWebhook(webhook({
      targetKind: 'tunnel',
      events: ['tunnel.runtime_error', 'p2p.connected'],
      body: '{"tunnel":"{{tunnel.id}}"}',
    }), catalog)).toContainEqual({ field: 'body', code: 'unavailableVariable', key: 'tunnel.id' });
  });

  test('does not apply POST body validation to GET requests', () => {
    const issues = validateWebhook(webhook({ method: 'GET', body: '{invalid {{missing.value}}' }), catalog);
    expect(issues.filter((issue) => issue.field === 'body')).toEqual([]);
  });

  test('accepts a complete client Webhook configuration', () => {
    expect(validateWebhook(webhook({
      targetMode: 'selected',
      targetIds: ['client-a'],
      url: 'https://hooks.example.test/{{client.id}}?delivery={{delivery.id}}',
      headers: [{ id: 'event', key: 'X-Event', value: '{{event.name.en-US}}' }],
    }), catalog)).toEqual([]);
  });

  test.each([-1, 86401, 1.5, NaN])('rejects invalid notification interval %s', (minIntervalSeconds) => {
    expect(validateWebhook(webhook({ minIntervalSeconds }), catalog)).toContainEqual({ field: 'minIntervalSeconds', code: 'invalidInterval' });
  });

  test.each([0, 300, 86400])('accepts notification interval %s', (minIntervalSeconds) => {
    expect(validateWebhook(webhook({ minIntervalSeconds }), catalog)).toEqual([]);
  });

  test('preserves the notification interval through API conversion and defaults new Webhooks to unlimited', () => {
    const config = webhook({ minIntervalSeconds: 300 });
    const input = activityWebhookToAPI(config);
    expect(input.min_interval_seconds).toBe(300);
    expect(activityWebhookFromAPI({
      ...input,
      revision: 1,
      calls_24h: 0,
      last_status: 'idle',
      consecutive_failures: 0,
      created_at: config.createdAt,
      updated_at: config.updatedAt,
    }).minIntervalSeconds).toBe(300);
    expect(createEmptyWebhook(catalog).minIntervalSeconds).toBe(0);
  });
});
