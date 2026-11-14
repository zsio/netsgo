import { createHash, createPrivateKey, createPublicKey, sign, verify } from 'node:crypto';
import { readFile, readdir, stat, mkdir, writeFile } from 'node:fs/promises';
import { createReadStream } from 'node:fs';
import { spawnSync } from 'node:child_process';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

export const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
export const appId = 'NetsGo.Desktop';
export const vpkVersion = '1.2.161';
export const platforms = {
  'osx-arm64': { os: 'macos-15', triple: 'aarch64-apple-darwin', goos: 'darwin', goarch: 'arm64' },
  'osx-x64': { os: 'macos-15-intel', triple: 'x86_64-apple-darwin', goos: 'darwin', goarch: 'amd64' },
  'win-x64': { os: 'windows-2022', triple: 'x86_64-pc-windows-msvc', goos: 'windows', goarch: 'amd64' },
};

export function cloneReleaseIndex(repository, directory, env = {}) {
  // The index is pushed to another hosting provider, which may reject shallow history.
  const result = spawnSync('git', ['clone', '--single-branch', '--branch', 'release-index', repository, directory], {
    env: { ...process.env, ...env }, encoding: 'utf8',
  });
  if (result.error) throw result.error;
  if (result.status !== 0) throw new Error(`Could not clone release index: ${result.stderr}`);
}

export function parseTag(tag) {
  const match = /^v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-beta\.([1-9]\d*))?$/.exec(tag);
  if (!match) throw new Error(`Invalid release tag: ${tag}`);
  return { numbers: match.slice(1, 4).map(BigInt), beta: match[4] ? BigInt(match[4]) : null, channel: match[4] ? 'beta' : 'stable' };
}

export function compareTags(a, b) {
  const left = parseTag(a), right = parseTag(b);
  for (let i = 0; i < 3; i++) if (left.numbers[i] !== right.numbers[i]) return left.numbers[i] > right.numbers[i] ? 1 : -1;
  if (left.beta === right.beta) return 0;
  if (left.beta === null) return 1;
  if (right.beta === null) return -1;
  return left.beta > right.beta ? 1 : -1;
}

export function signingMode(value = 'unsigned') {
  if (!['unsigned', 'required'].includes(value)) throw new Error(`Invalid desktop signing mode: ${value}`);
  return value;
}

export function releasePlan(plan, tag) {
  parseTag(tag);
  const signing = signingMode(plan.signing);
  if (plan.tag !== tag) return { enabled: false, signing, matrix: { include: [] } };
  if (!Array.isArray(plan.targets) || !plan.targets.length || new Set(plan.targets).size !== plan.targets.length) {
    throw new Error('Desktop release targets must be nonempty and unique');
  }
  return { enabled: true, signing, matrix: { include: plan.targets.map(target => {
    if (!platforms[target]) throw new Error(`Unsupported desktop target: ${target}`);
    return { target, ...platforms[target] };
  }) } };
}

export async function sha256File(file) {
  const hash = createHash('sha256');
  for await (const chunk of createReadStream(file)) hash.update(chunk);
  return hash.digest('hex');
}

export function signFeed(feed, privateKey) {
  const payload = Buffer.from(JSON.stringify(feed));
  return { payload: payload.toString('base64'), signature: sign(null, payload, privateKey).toString('base64') };
}

export function readSignedFeed(envelope, publicKey) {
  const payload = Buffer.from(envelope.payload, 'base64');
  if (!verify(null, payload, publicKey, Buffer.from(envelope.signature, 'base64'))) throw new Error('Desktop feed signature mismatch');
  return JSON.parse(payload);
}

export function mergeFeed(previous, incoming) {
  if (!previous) return incoming;
  if (previous.schema !== 1 || previous.product !== incoming.product || previous.target !== incoming.target) {
    throw new Error('Existing desktop feed has a different identity');
  }
  const next = incoming.releases[0];
  const channel = parseTag(next.tag).channel;
  const old = previous.releases.find(release => parseTag(release.tag).channel === channel);
  if (old) {
    const cmp = compareTags(next.tag, old.tag);
    if (cmp < 0) return previous; // Out-of-order CI completion must never roll a channel back.
    if (cmp === 0) {
      if (old.asset.SHA256 !== next.asset.SHA256) throw new Error('Cannot replace an existing desktop version with different content');
      return previous;
    }
  }
  return { ...incoming, releases: [...previous.releases.filter(release => parseTag(release.tag).channel !== channel), next] };
}

export async function signingKey() {
  const raw = process.env.NETSGO_RELEASE_SIGNING_KEY_PEM;
  if (!raw) throw new Error('NETSGO_RELEASE_SIGNING_KEY_PEM is required');
  const privateKey = createPrivateKey(raw);
  const script = await readFile(path.join(root, 'scripts/common-update.sh'), 'utf8');
  const embedded = script.match(/NETSGO_RELEASE_PUBLIC_KEY_PEM='([^']+)'/s)?.[1];
  const actual = createPublicKey(privateKey).export({ type: 'spki', format: 'der' });
  const expected = createPublicKey(embedded).export({ type: 'spki', format: 'der' });
  if (!actual.equals(expected)) throw new Error('Desktop signing key does not match the embedded public key');
  return privateKey;
}

export async function collectRelease(distDir, target, tag, repo, cnbSlug) {
  const channel = `${parseTag(tag).channel}-${target}`;
  const dir = path.join(distDir, target);
  const native = JSON.parse(await readFile(path.join(dir, `releases.${channel}.json`), 'utf8'));
  const full = native.Assets.filter(asset => asset.Type === 'Full' && asset.Version === tag.slice(1));
  if (full.length !== 1 || full[0].PackageId !== appId) throw new Error(`Expected one ${target} update package for ${tag}`);
  const asset = full[0];
  if (!/^[A-Za-z0-9._-]+-full\.nupkg$/.test(asset.FileName) || asset.FileName.includes('..')) throw new Error('Invalid package filename');
  const packagePath = path.join(dir, asset.FileName);
  const hash = await sha256File(packagePath);
  if (asset.SHA256?.toLowerCase() !== hash || (await stat(packagePath)).size !== asset.Size) throw new Error(`${target} update package checksum mismatch`);
  const files = (await readdir(dir)).filter(name => /\.(nupkg|zip|pkg|exe|json|txt)$/.test(name));
  const urls = [
    { provider: 'cnb', url: `https://cnb.cool/${cnbSlug}/-/releases/download/${tag}/${encodeURIComponent(asset.FileName)}` },
    { provider: 'github', url: `https://github.com/${repo}/releases/download/${tag}/${encodeURIComponent(asset.FileName)}` },
  ];
  return { dir, files, feed: { schema: 1, product: 'netsgo-desktop', target, releases: [{ tag, asset, urls }] } };
}

export async function writeFeed(outDir, incoming, key) {
  const dest = path.join(outDir, 'updates/desktop-v1', `${incoming.target}.json`);
  let previous;
  try { previous = readSignedFeed(JSON.parse(await readFile(dest, 'utf8')), createPublicKey(key)); }
  catch (error) { if (error.code !== 'ENOENT') throw error; }
  const merged = mergeFeed(previous, incoming);
  await mkdir(path.dirname(dest), { recursive: true });
  await writeFile(dest, `${JSON.stringify(signFeed(merged, key), null, 2)}\n`);
}

if (import.meta.main) {
  if (process.argv[2] !== 'plan') throw new Error('Usage: bun scripts/desktop-release.mjs plan <tag>');
  const result = releasePlan(JSON.parse(await readFile(path.join(root, 'client-desktop/release.json'), 'utf8')), process.argv[3]);
  console.log(`enabled=${result.enabled}`);
  console.log(`signing=${result.signing}`);
  console.log(`matrix=${JSON.stringify(result.matrix)}`);
}
