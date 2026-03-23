const SERVER_ADDR_PROTOCOLS = new Set(['http:', 'https:']);

export const SERVER_ADDR_PLACEHOLDER = '例如: https://tunnel.example.com';
export const SERVER_ADDR_HELP_TEXT = '仅支持 http://、https://。请输入基础连接地址，不要包含路径、查询参数、锚点或用户信息。推荐通过 nginx/caddy 等反向代理启用 HTTPS。';

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

function hasUserInfo(raw: string) {
  return /^[a-z][a-z0-9+.-]*:\/\/[^/]*@/i.test(raw);
}

function validateParsedServerAddr(raw: string, url: URL) {
  if (!isSupportedServerAddrProtocol(url.protocol)) {
    return '仅支持 http://、https://';
  }

  if ((url.pathname && url.pathname !== '/') || url.search || url.hash) {
    return '请输入基础连接地址，不要包含路径、查询参数或锚点';
  }

  if (hasUserInfo(raw) || url.username || url.password) {
    return '请输入基础连接地址，不要包含用户信息';
  }

  const hostname = url.hostname.toLowerCase();
  const isLocalhost = hostname === 'localhost';
  const isIpv4 = /^(25[0-5]|2[0-4]\d|1?\d?\d)(\.(25[0-5]|2[0-4]\d|1?\d?\d)){3}$/.test(hostname);
  const isIpv6 = hostname.includes(':');

  if (!isLocalhost && !isIpv4 && !isIpv6) {
    const labels = hostname.split('.');
    const isFQDN = labels.length >= 2 && labels.every((label) => (
      label.length > 0 &&
      !label.startsWith('-') &&
      !label.endsWith('-') &&
      /^[a-z0-9-]+$/.test(label)
    ));

    if (!isFQDN) {
      return '主机名必须是 FQDN、localhost、IPv4 或 IPv6 字面量';
    }
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
    return { error: '请输入有效的完整 URL（需包含 http:// 或 https://）' as const };
  }

  const error = validateParsedServerAddr(trimmed, url);
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
  const isIp = /^(25[0-5]|2[0-4]\d|1?\d?\d)(\.(25[0-5]|2[0-4]\d|1?\d?\d)){3}$/.test(hostname) || hostname.includes(':');
  const isLocalhost = hostname === 'localhost';
  const isDomain = !isIp && !isLocalhost && /\.[a-zA-Z]{2,}$/.test(hostname);

  return {
    isSecure: result.url.protocol === 'https:',
    hostKind: isDomain ? 'domain' : isIp ? 'ip' : isLocalhost ? 'localhost' : 'local',
  };
}
