#!/usr/bin/env node

import { createHash } from 'node:crypto';
import { mkdir, readFile, stat, writeFile } from 'node:fs/promises';
import path from 'node:path';

const [, , command] = process.argv;

const stableTagRe = /^v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/;
const betaTagRe = /^v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)-beta\.([1-9]\d*)$/;

function env(name, fallback = '') {
  return process.env[name] || fallback;
}

function requiredEnv(name) {
  const value = env(name);
  if (!value) throw new Error(`${name} is required`);
  return value;
}

function normalizeTag(tag) {
  if (!tag) return '';
  return tag.startsWith('refs/tags/') ? tag.slice('refs/tags/'.length) : tag;
}

function releaseChannel(tag) {
  if (stableTagRe.test(tag)) return 'stable';
  if (betaTagRe.test(tag)) return 'beta';
  throw new Error(`invalid release tag: ${tag}`);
}

function artifactVersion(tag) {
  return tag.slice(1);
}

function parseAssetName(name, version) {
  const prefix = `netsgo_${version}_`;
  const suffix = '.tar.gz';
  if (!name.startsWith(prefix) || !name.endsWith(suffix)) return null;
  const platform = name.slice(prefix.length, -suffix.length);
  const parts = platform.split('_');
  if (parts.length !== 2) return null;
  return { os: parts[0], arch: parts[1] };
}

async function sha256File(filePath) {
  const data = await readFile(filePath);
  return createHash('sha256').update(data).digest('hex');
}

function githubDownloadURL(repo, tag, name) {
  return `https://github.com/${repo}/releases/download/${tag}/${encodeURIComponent(name)}`;
}

function cnbDownloadURL(repoSlug, tag, name) {
  const encodedRepo = repoSlug.split('/').map(encodeURIComponent).join('/');
  return `https://cnb.cool/${encodedRepo}/-/releases/download/${encodeURIComponent(tag)}/${encodeURIComponent(name)}`;
}

function githubReleaseDetailURL(repo, tag) {
  return `https://raw.githubusercontent.com/${repo}/release-index/updates/index-v1/releases/${tag}.json`;
}

function cnbReleaseDetailURL(repoSlug, tag) {
  const encodedRepo = repoSlug.split('/').map(encodeURIComponent).join('/');
  return `https://cnb.cool/${encodedRepo}/-/raw/release-index/updates/index-v1/releases/${encodeURIComponent(tag)}.json`;
}

function parseUploadedAssetSet(value) {
  return new Set(value.split(',').map((name) => name.trim()).filter(Boolean));
}

function hasUploadedAssetFilter() {
  return Object.hasOwn(process.env, 'CNB_UPLOADED_ASSETS');
}

async function readExistingLatest(outDir) {
  try {
    const raw = await readFile(path.join(outDir, 'updates', 'index-v1', 'latest.json'), 'utf8');
    const parsed = JSON.parse(raw);
    if (parsed?.schema === 1 && parsed?.project === 'netsgo' && parsed?.channels) return parsed;
  } catch {
    // No existing index is fine for the first release.
  }
  return { schema: 1, project: 'netsgo', channels: {} };
}

async function buildIndex() {
  const tag = normalizeTag(requiredEnv('GITHUB_REF_NAME') || requiredEnv('GITHUB_REF'));
  const channel = releaseChannel(tag);
  const version = artifactVersion(tag);
  const repo = requiredEnv('GITHUB_REPOSITORY');
  const distDir = env('DIST_DIR', 'dist');
  const outDir = env('OUT_DIR', 'release-index');
  const cnbRepoSlug = env('CNB_REPO_SLUG');
  const cnbUploadedAssets = hasUploadedAssetFilter() ? parseUploadedAssetSet(env('CNB_UPLOADED_ASSETS')) : null;
  const generatedAt = new Date().toISOString();

  const checksumsPath = path.join(distDir, 'checksums.txt');
  const rawSigPath = path.join(distDir, 'checksums.txt.sig');
  const sshSigPath = path.join(distDir, 'checksums.txt.sshsig');
  await stat(checksumsPath);
  await stat(rawSigPath);
  await stat(sshSigPath);

  const checksums = await readFile(checksumsPath, 'utf8');
  const checksumByName = new Map();
  for (const line of checksums.split(/\r?\n/)) {
    if (!line.trim()) continue;
    const [hash, name] = line.trim().split(/\s+/, 2);
    if (hash && name) checksumByName.set(name, hash.toLowerCase());
  }

  const providerURLs = (name) => {
    const urls = [{ provider: 'github', url: githubDownloadURL(repo, tag, name), requires_auth: false }];
    if (cnbRepoSlug && (!cnbUploadedAssets || cnbUploadedAssets.has(name))) {
      urls.unshift({ provider: 'cnb', url: cnbDownloadURL(cnbRepoSlug, tag, name), requires_auth: false });
    }
    return urls;
  };

  const assets = [];
  for (const [name, checksum] of checksumByName) {
    const platform = parseAssetName(name, version);
    if (!platform) continue;
    const filePath = path.join(distDir, name);
    const fileInfo = await stat(filePath);
    const actual = await sha256File(filePath);
    if (actual !== checksum) throw new Error(`${name} checksum mismatch: manifest=${checksum} actual=${actual}`);
    assets.push({
      name,
      os: platform.os,
      arch: platform.arch,
      size: fileInfo.size,
      sha256: checksum,
      urls: providerURLs(name),
    });
  }
  if (assets.length === 0) throw new Error('release detail must contain at least one installable archive asset');
  assets.sort((a, b) => `${a.os}/${a.arch}`.localeCompare(`${b.os}/${b.arch}`));

  const release = {
    schema: 1,
    project: 'netsgo',
    version: tag,
    prerelease: channel === 'beta',
    generated_at: generatedAt,
    checksum_asset: {
      name: 'checksums.txt',
      urls: providerURLs('checksums.txt'),
    },
    signature_assets: {
      ed25519: {
        name: 'checksums.txt.sig',
        urls: providerURLs('checksums.txt.sig'),
      },
      sshsig: {
        name: 'checksums.txt.sshsig',
        urls: providerURLs('checksums.txt.sshsig'),
      },
    },
    assets,
  };
  if (!release.checksum_asset.urls.length) throw new Error('release detail checksum asset must include at least one URL');
  if (!release.signature_assets.ed25519.urls.length) throw new Error('release detail must include checksums.txt.sig URL');
  if (!release.signature_assets.sshsig.urls.length) throw new Error('release detail must include checksums.txt.sshsig URL');

  const releasesDir = path.join(outDir, 'updates', 'index-v1', 'releases');
  await mkdir(releasesDir, { recursive: true });
  await writeFile(path.join(releasesDir, `${tag}.json`), JSON.stringify(release, null, 2) + '\n');

  const latest = await readExistingLatest(outDir);
  latest.generated_at = generatedAt;
  const releaseURLs = [{ provider: 'github', url: githubReleaseDetailURL(repo, tag) }];
  if (cnbRepoSlug) releaseURLs.unshift({ provider: 'cnb', url: cnbReleaseDetailURL(cnbRepoSlug, tag) });
  latest.channels[channel] = {
    latest: tag,
    release_urls: releaseURLs,
  };
  await writeFile(path.join(outDir, 'updates', 'index-v1', 'latest.json'), JSON.stringify(latest, null, 2) + '\n');
}

async function main() {
  switch (command) {
    case 'build':
      await buildIndex();
      break;
    default:
      throw new Error(`unknown command: ${command || ''}`);
  }
}

main().catch((error) => {
  console.error(error);
  process.exit(1);
});
