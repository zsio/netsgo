#!/usr/bin/env bun
import { createHash } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { mkdtemp, readFile, rm, stat } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { appId, cloneReleaseIndex, collectRelease, releasePlan, root, sha256File, signingKey, writeFeed } from './desktop-release.mjs';

// This script only runs in the tag workflow, under the shared release-index job lock.
const required = name => {
  if (!process.env[name]) throw new Error(`${name} is required to publish desktop updates`);
  return process.env[name];
};
const tag = required('GITHUB_REF_NAME');
const repo = required('GITHUB_REPOSITORY');
const ghToken = required('GITHUB_TOKEN');
const cnbToken = required('CNB_PASSWORD');
const cnbUser = required('CNB_USERNAME');
const cnbRepo = required('CNB_REPO_URL').replace(/\.git$/, '');
if (!/^https:\/\/cnb\.cool\/[\w.-]+\/[\w.-]+$/.test(cnbRepo)) throw new Error('Invalid CNB repository URL');
const cnbSlug = cnbRepo.slice('https://cnb.cool/'.length).toLowerCase();
const plan = releasePlan(JSON.parse(await readFile(path.join(root, 'client-desktop/release.json'), 'utf8')), tag);
if (!plan.enabled) throw new Error('This tag does not request a desktop release');
const key = await signingKey();
const dist = path.resolve(process.env.DESKTOP_DIST_DIR || 'desktop-dist');

function run(command, args, cwd, extraEnv = {}, allowFailure = false) {
  const result = spawnSync(command, args, { cwd, env: { ...process.env, ...extraEnv }, encoding: 'utf8' });
  if (result.error) throw result.error;
  if (result.status && !allowFailure) throw new Error(`${command} failed: ${result.stderr}`);
  return result;
}
function gitAuth(username, token) {
  return { GIT_CONFIG_COUNT: '1', GIT_CONFIG_KEY_0: 'http.extraHeader',
    GIT_CONFIG_VALUE_0: `Authorization: Basic ${Buffer.from(`${username}:${token}`).toString('base64')}` };
}
async function retry(operation) {
  for (let attempt = 0; ; attempt++) {
    try { return await operation(); }
    catch (error) {
      if (attempt === 2) throw error;
      await new Promise(resolve => setTimeout(resolve, (attempt + 1) * 1500));
    }
  }
}
async function api(url, token, method = 'GET', body) {
  const mediaType = new URL(url).hostname === 'api.cnb.cool' ? 'application/vnd.cnb.api+json' : 'application/vnd.github+json';
  const response = await fetch(url, { method, headers: { Authorization: `Bearer ${token}`,
    Accept: mediaType, 'Content-Type': mediaType, 'User-Agent': 'NetsGo-Release' },
  body: body ? JSON.stringify(body) : undefined, signal: AbortSignal.timeout(30000) });
  if (response.status === 404) return null;
  if (!response.ok) throw new Error(`Release API returned HTTP ${response.status}`);
  const text = await response.text();
  return text ? JSON.parse(text) : {};
}
async function publicHash(url) {
  const response = await fetch(url, { signal: AbortSignal.timeout(180000) });
  if (response.status === 404) return null;
  if (!response.ok) throw new Error(`Public asset unavailable: HTTP ${response.status}`);
  const hash = createHash('sha256');
  for await (const chunk of response.body) hash.update(chunk);
  return hash.digest('hex');
}

const indexDir = await mkdtemp(path.join(os.tmpdir(), 'netsgo-desktop-index-'));
const ghRepo = `https://github.com/${repo}.git`;
const ghAuth = gitAuth('x-access-token', ghToken);
try {
  const exists = run('git', ['ls-remote', '--exit-code', '--heads', ghRepo, 'release-index'], root, ghAuth, true);
  if (exists.status === 0) {
    cloneReleaseIndex(ghRepo, indexDir, ghAuth);
  } else if (exists.status === 2) {
    run('git', ['init', '-b', 'release-index', indexDir], root);
  } else { throw new Error('Could not read the current release index'); }

  const releases = await Promise.all(plan.matrix.include.map(({ target }) => collectRelease(dist, target, tag, repo, cnbSlug)));
  const names = releases.flatMap(release => release.files);
  if (new Set(names).size !== names.length) throw new Error('Desktop artifacts must have unique names across targets');
  // Reject signature errors or same-version mutations before touching public assets.
  for (const release of releases) await writeFeed(indexDir, release.feed, key);

  const ghRelease = await retry(() => api(`https://api.github.com/repos/${repo}/releases/tags/${tag}`, ghToken));
  if (!ghRelease || ghRelease.draft) throw new Error('The tag release must be published before uploading desktop artifacts');
  const cnbApi = `https://api.cnb.cool/${cnbSlug}/-/releases`;
  let cnbRelease = await retry(() => api(`${cnbApi}/tags/${tag}`, cnbToken));
  if (!cnbRelease) cnbRelease = await api(cnbApi, cnbToken, 'POST', {
    tag_name: tag, target_commitish: required('GITHUB_SHA'), name: tag,
    body: 'NetsGo release mirrored from GitHub Actions.', draft: false,
    prerelease: tag.includes('-beta.'), make_latest: tag.includes('-beta.') ? 'false' : 'true',
  });
  if (!cnbRelease?.id) throw new Error('CNB release ID missing');

  for (const release of releases) {
    for (const name of release.files) {
      const file = path.join(release.dir, name);
      const digest = await sha256File(file);
      const ghUrl = `https://github.com/${repo}/releases/download/${tag}/${encodeURIComponent(name)}`;
      const existing = ghRelease.assets.find(asset => asset.name === name);
      if (existing) {
        if (await retry(() => publicHash(ghUrl)) !== digest) throw new Error(`Refusing to overwrite GitHub asset: ${name}`);
      } else {
        run('gh', ['release', 'upload', tag, file, '--repo', repo], root, { GH_TOKEN: ghToken });
      }
      const cnbUrl = `${cnbRepo}/-/releases/download/${tag}/${encodeURIComponent(name)}`;
      const cnbDigest = await retry(() => publicHash(cnbUrl));
      if (cnbDigest && cnbDigest !== digest) throw new Error(`Refusing to overwrite CNB asset: ${name}`);
      if (!cnbDigest) await retry(async () => {
        const upload = await api(`${cnbApi}/${cnbRelease.id}/asset-upload-url`, cnbToken, 'POST', {
          asset_name: name, overwrite: true, size: (await stat(file)).size, ttl: 0,
        });
        const response = await fetch(upload.upload_url, { method: 'PUT', body: Bun.file(file), signal: AbortSignal.timeout(180000) });
        if (!response.ok) throw new Error(`CNB upload failed: HTTP ${response.status}`);
        const confirmation = new URL(upload.verify_url, 'https://api.cnb.cool');
        if (confirmation.origin !== 'https://api.cnb.cool') throw new Error('Invalid CNB confirmation origin');
        confirmation.searchParams.set('ttl', '0');
        await api(confirmation, cnbToken, 'POST');
      });
      // Both mirrors must be readable without authentication before advertising an update.
      await retry(async () => {
        const hashes = await Promise.all([publicHash(ghUrl), publicHash(cnbUrl)]);
        if (hashes.some(hash => hash !== digest)) throw new Error(`Public download verification failed: ${name}`);
      });
      console.log(`Verified desktop artifact on both mirrors: ${name}`);
    }
  }

  run('git', ['config', 'user.name', 'github-actions[bot]'], indexDir);
  run('git', ['config', 'user.email', 'github-actions[bot]@users.noreply.github.com'], indexDir);
  run('git', ['add', 'updates/desktop-v1'], indexDir);
  if (run('git', ['diff', '--cached', '--quiet'], indexDir, {}, true).status !== 0) {
    run('git', ['commit', '-m', `desktop release index ${tag}`], indexDir);
  }
  // The other publisher uses the same CI concurrency group and preserves this directory.
  run('git', ['push', ghRepo, 'HEAD:refs/heads/release-index'], indexDir, ghAuth);
  await retry(async () => run('git', ['push', '--force', `${cnbRepo}.git`, 'HEAD:refs/heads/release-index'], indexDir, gitAuth(cnbUser, cnbToken)));
  console.log(`Published ${appId} ${tag} update feeds`);
} finally {
  await rm(indexDir, { recursive: true, force: true });
}
