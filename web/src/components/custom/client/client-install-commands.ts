export const INSTALL_SCRIPT_URL = 'https://netsgo.zs.uy/install.sh';
export const CLIENT_DOCKER_REPOSITORY = 'zsio/netsgo';
export const CLIENT_CNB_DOCKER_REPOSITORY = 'docker.cnb.cool/zsio/netsgo';

export type ClientReleaseChannel = 'stable' | 'beta';

export function hasKnownClientInstallVersion(version?: string) {
  return (version?.trim() ?? '') !== '';
}

export function clientReleaseChannelForVersion(version?: string): ClientReleaseChannel {
  const normalized = version?.trim() ?? '';
  return /^v?(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)-beta\.[1-9]\d*$/.test(normalized)
    ? 'beta'
    : 'stable';
}

export function clientDockerImageForVersion(version?: string) {
  if (clientReleaseChannelForVersion(version) !== 'beta') {
    return `${CLIENT_DOCKER_REPOSITORY}:latest`;
  }

  const normalized = version?.trim().replace(/^v/, '');
  return normalized
    ? `${CLIENT_DOCKER_REPOSITORY}:${normalized}`
    : `${CLIENT_DOCKER_REPOSITORY}:latest`;
}

export function clientCNBDockerImageForVersion(version?: string) {
  return clientDockerImageForVersion(version).replace(
    CLIENT_DOCKER_REPOSITORY,
    CLIENT_CNB_DOCKER_REPOSITORY,
  );
}

export function clientInstallChannelArgForVersion(version?: string) {
  return clientReleaseChannelForVersion(version) === 'beta' ? '--channel beta' : '';
}
