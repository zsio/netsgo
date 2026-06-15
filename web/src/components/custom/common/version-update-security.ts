export const TRUSTED_RELEASE_URL = 'https://github.com/zsio/netsgo/releases';
export const TRUSTED_UPGRADE_COMMAND = 'curl -fsSL https://netsgo.zs.uy/upgrade.sh | sh -s -- -y';

export function safeReleaseUrl(value?: string) {
  if (!value) {
    return TRUSTED_RELEASE_URL;
  }

  try {
    const url = new URL(value);
    const safePath = url.pathname === '/zsio/netsgo/releases' || url.pathname.startsWith('/zsio/netsgo/releases/');
    if (url.protocol === 'https:' && url.hostname === 'github.com' && safePath) {
      return url.toString();
    }
  } catch {
    // Fall through to the trusted default.
  }

  return TRUSTED_RELEASE_URL;
}

export function safeUpgradeCommand(command?: string) {
  return command?.trim() === TRUSTED_UPGRADE_COMMAND ? TRUSTED_UPGRADE_COMMAND : null;
}
