#!/usr/bin/env bun
// Recovery only: copy an existing signed index without rebuilding or resigning a release.
import assert from 'node:assert/strict';
import { createHash, createPublicKey } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { mkdtemp, readFile, rm } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { cloneReleaseIndex, parseTag, platforms, readSignedFeed, root } from './desktop-release.mjs';

const tag = process.argv[2];
parseTag(tag);
const repo = process.env.GITHUB_REPOSITORY;
const cnb = process.env.CNB_REPO_URL?.replace(/\.git$/, '');
assert.match(repo || '', /^[\w.-]+\/[\w.-]+$/);
assert.match(cnb || '', /^https:\/\/cnb\.cool\/[\w.-]+\/[\w.-]+$/);
assert.ok(process.env.CNB_USERNAME && process.env.CNB_PASSWORD, 'CNB credentials are required');
const source = await readFile(path.join(root, 'scripts/common-update.sh'), 'utf8');
const key = createPublicKey(source.match(/NETSGO_RELEASE_PUBLIC_KEY_PEM='([^']+)'/s)[1]);
const index = await mkdtemp(path.join(os.tmpdir(), 'netsgo-index-repair-'));
try {
  cloneReleaseIndex(`https://github.com/${repo}.git`, index);
  const targets = [];
  for (const target of Object.keys(platforms)) {
    let envelope;
    try { envelope = JSON.parse(await readFile(path.join(index, 'updates/desktop-v1', `${target}.json`), 'utf8')); }
    catch (error) { if (error.code === 'ENOENT') continue; throw error; }
    const feed = readSignedFeed(envelope, key);
    assert.equal(feed.product, 'netsgo-desktop');
    assert.equal(feed.target, target);
    const release = feed.releases.find(release => release.tag === tag);
    if (!release) continue;
    await Promise.all(release.urls.map(async ({ url, provider }) => {
      assert.ok(url.startsWith('https://'));
      const response = await fetch(url, { signal: AbortSignal.timeout(180000) });
      assert.equal(response.status, 200, `${target}/${provider} is unavailable`);
      const hash = createHash('sha256');
      let size = 0;
      for await (const chunk of response.body) { hash.update(chunk); size += chunk.length; }
      assert.equal(size, release.asset.Size);
      assert.equal(hash.digest('hex'), release.asset.SHA256.toLowerCase());
    }));
    targets.push(target);
    console.log(`Verified signed ${target} feed and both download mirrors`);
  }
  assert.ok(targets.length, `No published desktop feed contains ${tag}`);
  const token = Buffer.from(`${process.env.CNB_USERNAME}:${process.env.CNB_PASSWORD}`).toString('base64');
  const pushed = spawnSync('git', ['push', '--force', `${cnb}.git`, 'HEAD:refs/heads/release-index'], {
    cwd: index, encoding: 'utf8', env: { ...process.env, GIT_CONFIG_COUNT: '1',
      GIT_CONFIG_KEY_0: 'http.extraHeader', GIT_CONFIG_VALUE_0: `Authorization: Basic ${token}` },
  });
  if (pushed.error) throw pushed.error;
  if (pushed.status !== 0) throw new Error(`Could not repair CNB index: ${pushed.stderr}`);
  console.log(`Repaired CNB release index for ${tag}; existing signed bytes retained`);
} finally { await rm(index, { recursive: true, force: true }); }
