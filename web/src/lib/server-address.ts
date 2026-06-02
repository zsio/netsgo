const SERVER_ADDR_PROTOCOLS = new Set(['http:', 'https:']);

export const SERVER_ADDR_PLACEHOLDER = 'e.g. https://tunnel.example.com';
export const SERVER_ADDR_HELP_TEXT = 'Only http:// and https:// are supported. Enter a base connection address without path, query, fragment, or user info. HTTPS through nginx/caddy or another reverse proxy is recommended.';

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
    return 'Only http:// and https:// are supported.';
  }

  if ((url.pathname && url.pathname !== '/') || url.search || url.hash) {
    return 'Enter a base connection address without path, query, or fragment.';
  }

  if (hasUserInfo(raw) || url.username || url.password) {
    return 'Enter a base connection address without user info.';
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
      return 'Hostname must be an FQDN, localhost, IPv4, or IPv6 literal.';
    }
  }

  return null;
}

function parseValidatedServerAddr(value: string) {
  const trimmed = value.trim();
  if (!trimmed) {
    return { error: 'Enter a valid Client connection address.' as const };
  }

  const url = parseServerAddr(trimmed);
  if (!url) {
    return { error: 'Enter a valid full URL that includes http:// or https://.' as const };
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
