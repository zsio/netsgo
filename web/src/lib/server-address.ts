const SERVER_ADDR_PROTOCOLS = new Set(['http:', 'https:', 'ws:', 'wss:']);

export const SERVER_ADDR_PLACEHOLDER = '例如: https://tunnel.example.com';
export const SERVER_ADDR_HELP_TEXT = '支持 http://、https://、ws://、wss://。请输入基础连接地址，不要包含路径、查询参数或锚点。推荐通过 nginx/caddy 等反向代理启用 HTTPS 或 WSS。';

type ServerAddrHostKind = 'domain' | 'ip' | 'localhost' | 'local';

interface ServerAddrInfo {
  isSecure: boolean;
  hostKind: ServerAddrHostKind;
}

function parseServerAddr(value: string): URL | null {
  const trimmed = value.trim();
  if (!trimmed) {
    return null;
  }

  try {
    return new URL(trimmed);
  } catch {
    return null;
  }
}

function isSupportedServerAddrProtocol(protocol: string) {
  return SERVER_ADDR_PROTOCOLS.has(protocol);
}

function normalizeParsedServerAddr(url: URL) {
  return `${url.protocol}//${url.host}`;
}

function validateParsedServerAddr(url: URL) {
  if (!isSupportedServerAddrProtocol(url.protocol)) {
    return '仅支持 http://、https://、ws://、wss://';
  }

  if ((url.pathname && url.pathname !== '/') || url.search || url.hash) {
    return '请输入基础连接地址，不要包含路径、查询参数或锚点';
  }

  return null;
}

function parseValidatedServerAddr(value: string) {
  const trimmed = value.trim();
  if (!trimmed) {
    return { error: '请填写有效的 Client 连接地址' as const };
  }

  const url = parseServerAddr(trimmed);
  if (!url) {
    return { error: '请输入有效的完整 URL（需包含 http://、https://、ws:// 或 wss://）' as const };
  }

  const error = validateParsedServerAddr(url);
  if (error) {
    return { error };
  }

  return {
    url,
    normalized: normalizeParsedServerAddr(url),
  };
}

export function getServerAddrValidationError(value: string) {
  const result = parseValidatedServerAddr(value);
  return 'error' in result ? result.error : null;
}

export function normalizeServerAddr(value: string) {
  const result = parseValidatedServerAddr(value);
  return 'error' in result ? null : result.normalized;
}

export function getServerAddrInfo(value: string): ServerAddrInfo | null {
  const result = parseValidatedServerAddr(value);
  if ('error' in result) {
    return null;
  }

  const hostname = result.url.hostname;
  const isIp = /^\d{1,3}(\.\d{1,3}){3}$/.test(hostname) || hostname.startsWith('[');
  const isLocalhost = hostname === 'localhost';
  const isDomain = !isIp && !isLocalhost && /\.[a-zA-Z]{2,}$/.test(hostname);

  return {
    isSecure: result.url.protocol === 'https:' || result.url.protocol === 'wss:',
    hostKind: isDomain ? 'domain' : isIp ? 'ip' : isLocalhost ? 'localhost' : 'local',
  };
}
