#!/usr/bin/env node

import { createHash } from 'node:crypto';
import { mkdir, readFile, stat, writeFile } from 'node:fs/promises';
import path from 'node:path';

const [, , command] = process.argv;

function env(name, fallback = '') {
  return process.env[name] || fallback;
}

function requiredEnv(name) {
  const value = env(name);
  if (!value) {
    throw new Error(`${name} is required`);
  }
  return value;
}

function normalizeTag(tag) {
  if (!tag) return '';
  return tag.startsWith('refs/tags/') ? tag.slice('refs/tags/'.length) : tag;
}

function versionFromTag(tag) {
  return tag.startsWith('v') ? tag.slice(1) : tag;
}

function parseAssetName(name, version) {
  const prefix = `netsgo_${version}_`;
  const suffix = '.tar.gz';
  if (!name.startsWith(prefix) || !name.endsWith(suffix)) {
    return null;
  }
  const platform = name.slice(prefix.length, -suffix.length);
  const parts = platform.split('_');
  if (parts.length !== 2) {
    return null;
  }
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

function parseUploadedAssetSet(value) {
  return new Set(value.split(',').map((name) => name.trim()).filter(Boolean));
}

function hasUploadedAssetFilter() {
  return Object.hasOwn(process.env, 'CNB_UPLOADED_ASSETS');
}

async function buildIndex() {
  const tag = normalizeTag(requiredEnv('GITHUB_REF_NAME') || requiredEnv('GITHUB_REF'));
  const version = versionFromTag(tag);
  const repo = requiredEnv('GITHUB_REPOSITORY');
  const distDir = env('DIST_DIR', 'dist');
  const outDir = env('OUT_DIR', 'release-index');
  const cnbRepoSlug = env('CNB_REPO_SLUG');
  const cnbUploadedAssets = hasUploadedAssetFilter() ? parseUploadedAssetSet(env('CNB_UPLOADED_ASSETS')) : null;
  const generatedAt = new Date().toISOString();

  const checksumsPath = path.join(distDir, 'checksums.txt');
  const checksums = await readFile(checksumsPath, 'utf8');
  const checksumByName = new Map();
  for (const line of checksums.split(/\r?\n/)) {
    if (!line.trim()) continue;
    const [hash, name] = line.trim().split(/\s+/, 2);
    if (hash && name) checksumByName.set(name, hash.toLowerCase());
  }

  const assets = [];
  for (const [name, checksum] of checksumByName) {
    const platform = parseAssetName(name, version);
    if (!platform) continue;
    const filePath = path.join(distDir, name);
    const fileInfo = await stat(filePath);
    const actual = await sha256File(filePath);
    if (actual !== checksum) {
      throw new Error(`${name} checksum mismatch: manifest=${checksum} actual=${actual}`);
    }

    const urls = [
      {
        provider: 'github',
        url: githubDownloadURL(repo, tag, name),
        requires_auth: false,
      },
    ];
    if (cnbRepoSlug && (!cnbUploadedAssets || cnbUploadedAssets.has(name))) {
      urls.unshift({
        provider: 'cnb',
        url: cnbDownloadURL(cnbRepoSlug, tag, name),
        requires_auth: false,
      });
    }

    assets.push({
      name,
      os: platform.os,
      arch: platform.arch,
      size: fileInfo.size,
      sha256: checksum,
      urls,
    });
  }

  assets.sort((a, b) => `${a.os}/${a.arch}`.localeCompare(`${b.os}/${b.arch}`));

  const checksumURLs = [
    {
      provider: 'github',
      url: githubDownloadURL(repo, tag, 'checksums.txt'),
      requires_auth: false,
    },
  ];
  if (cnbRepoSlug && (!cnbUploadedAssets || cnbUploadedAssets.has('checksums.txt'))) {
    checksumURLs.unshift({
      provider: 'cnb',
      url: cnbDownloadURL(cnbRepoSlug, tag, 'checksums.txt'),
      requires_auth: false,
    });
  }

  const release = {
    schema: 1,
    project: 'netsgo',
    version: tag,
    normalized_version: version,
    prerelease: version.includes('-'),
    generated_at: generatedAt,
    source: {
      repository: repo,
      commit: env('GITHUB_SHA'),
      github_release: `https://github.com/${repo}/releases/tag/${tag}`,
      cnb_repository: cnbRepoSlug ? `https://cnb.cool/${cnbRepoSlug}` : undefined,
    },
    checksum_asset: {
      name: 'checksums.txt',
      sha256: await sha256File(checksumsPath),
      urls: checksumURLs,
    },
    assets,
  };

  const latest = {
    schema: 1,
    project: 'netsgo',
    channel: release.prerelease ? 'prerelease' : 'stable',
    latest: tag,
    generated_at: generatedAt,
    release_url: `./releases/${tag}.json`,
    release,
  };

  const releasesDir = path.join(outDir, 'updates', 'v1', 'releases');
  await mkdir(releasesDir, { recursive: true });
  await writeFile(path.join(releasesDir, `${tag}.json`), JSON.stringify(release, null, 2) + '\n');
  await writeFile(path.join(outDir, 'updates', 'v1', 'latest.json'), JSON.stringify(latest, null, 2) + '\n');
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
