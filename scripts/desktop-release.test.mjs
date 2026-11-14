import { describe, expect, test } from 'bun:test';
import { generateKeyPairSync } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { existsSync } from 'node:fs';
import { mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { pathToFileURL } from 'node:url';
import { appId, cloneReleaseIndex, compareTags, mergeFeed, releasePlan, readSignedFeed, signFeed, signingMode, writeFeed } from './desktop-release.mjs';

const item = (tag, hash = 'a'.repeat(64)) => ({ tag, asset: { PackageId: appId, SHA256: hash }, urls: [] });
const feed = (...releases) => ({ schema: 1, product: 'netsgo-desktop', target: 'osx-arm64', releases });

describe('desktop release policy', () => {
  test('index clone can be pushed to a mirror that rejects shallow updates', async () => {
    const dir = await mkdtemp(path.join(tmpdir(), 'netsgo-index-git-'));
    const git = (...args) => {
      const result = spawnSync('git', ['-c', 'commit.gpgsign=false', '-c', 'user.name=Fixture', '-c', 'user.email=test@example.invalid', ...args], { encoding: 'utf8' });
      if (result.error) throw result.error;
      return result;
    };
    const ok = (...args) => {
      const result = git(...args);
      if (result.status !== 0) throw new Error(result.stderr);
      return result.stdout.trim();
    };
    try {
      const origin = path.join(dir, 'origin.git'), mirror = path.join(dir, 'mirror.git');
      const source = path.join(dir, 'source'), shallow = path.join(dir, 'shallow'), full = path.join(dir, 'full');
      ok('init', '--bare', origin); ok('init', '--bare', mirror);
      ok('init', '-b', 'release-index', source);
      await writeFile(path.join(source, 'history'), 'root');
      ok('-C', source, 'add', '.'); ok('-C', source, 'commit', '-m', 'root');
      await writeFile(path.join(source, 'signed.json'), 'exact signed payload bytes');
      ok('-C', source, 'add', '.'); ok('-C', source, 'commit', '-m', 'index');
      ok('-C', source, 'push', origin, 'HEAD:refs/heads/release-index');
      ok('clone', '--depth', '1', '--branch', 'release-index', pathToFileURL(origin).href, shallow);
      const rejected = git('-C', shallow, 'push', mirror, 'HEAD:refs/heads/release-index');
      expect(rejected.status).not.toBe(0);
      expect(rejected.stderr).toContain('shallow');
      cloneReleaseIndex(pathToFileURL(origin).href, full);
      expect(existsSync(path.join(full, '.git/shallow'))).toBe(false);
      ok('-C', full, 'push', mirror, 'HEAD:refs/heads/release-index');
      expect(ok('--git-dir', mirror, 'show', 'refs/heads/release-index:signed.json')).toBe('exact signed payload bytes');
    } finally { await rm(dir, { recursive: true, force: true }); }
  }, 30000);
  test('a CLI-only tag never requests desktop artifacts', () => {
    const plan = { tag: 'v1.0.0', targets: ['osx-arm64', 'win-x64'] };
    expect(releasePlan(plan, 'v1.0.1').enabled).toBe(false);
    expect(releasePlan(plan, 'v1.0.0').matrix.include).toHaveLength(2);
    expect(releasePlan({ tag: '', targets: [] }, 'v1.0.0').enabled).toBe(false);
  });
  test('rejects unsupported or duplicate targets before release', () => {
    for (const targets of [[], ['wrong'], ['win-x64', 'win-x64']]) {
      expect(() => releasePlan({ tag: 'v1.0.0', targets }, 'v1.0.0')).toThrow();
    }
  });
  test('certificate-free releases are the default and remain independent of channels', () => {
    expect(releasePlan({ tag: 'v1.0.0', targets: ['osx-arm64'] }, 'v1.0.0').signing).toBe('unsigned');
    expect(releasePlan({ tag: 'v1.0.0-beta.1', signing: 'unsigned', targets: ['win-x64'] }, 'v1.0.0-beta.1').signing).toBe('unsigned');
    expect(releasePlan({ tag: 'v1.0.0', signing: 'required', targets: ['osx-arm64'] }, 'v1.0.0').signing).toBe('required');
  });
  test('unknown signing modes cannot silently skip certificate checks', () => {
    expect(() => signingMode('requried')).toThrow();
    expect(() => releasePlan({ tag: 'v1.0.0', signing: false, targets: ['win-x64'] }, 'v1.0.0')).toThrow();
  });
  test('orders release tags numerically with stable newer than its betas', () => {
    expect(compareTags('v1.0.0-beta.10', 'v1.0.0-beta.9')).toBe(1);
    expect(compareTags('v1.0.0', 'v1.0.0-beta.10')).toBe(1);
    expect(compareTags('v1.10.0', 'v1.9.0')).toBe(1);
    expect(() => compareTags('v1.0.0-dirty', 'v1.0.0')).toThrow();
  });
  test('advances only its own channel, never rolls backward on late CI', () => {
    const existing = feed(item('v1.2.0'), item('v1.3.0-beta.1'));
    expect(mergeFeed(existing, feed(item('v1.1.0')))).toEqual(existing);
    const updated = mergeFeed(existing, feed(item('v1.3.0-beta.2')));
    expect(updated.releases.map(release => release.tag)).toEqual(['v1.2.0', 'v1.3.0-beta.2']);
    expect(() => mergeFeed(existing, feed(item('v1.2.0', 'b'.repeat(64))))).toThrow();
  });
  test('signatures bind the complete feed; altered metadata is rejected', () => {
    const { privateKey, publicKey } = generateKeyPairSync('ed25519');
    const envelope = signFeed(feed(item('v1.0.0')), privateKey);
    expect(readSignedFeed(envelope, publicKey).releases[0].tag).toBe('v1.0.0');
    envelope.payload = Buffer.from(JSON.stringify(feed(item('v2.0.0')))).toString('base64');
    expect(() => readSignedFeed(envelope, publicKey)).toThrow();
  });
  test('desktop publish preserves other targets and the CLI index byte for byte', async () => {
    const { privateKey } = generateKeyPairSync('ed25519');
    const dir = await mkdtemp(path.join(tmpdir(), 'netsgo-feed-test-'));
    try {
      const sentinel = path.join(dir, 'updates/index-v1/latest.json');
      await mkdir(path.dirname(sentinel), { recursive: true });
      await writeFile(sentinel, 'existing CLI index');
      await writeFeed(dir, feed(item('v1.0.0')), privateKey);
      const original = await readFile(path.join(dir, 'updates/desktop-v1/osx-arm64.json'));
      await writeFeed(dir, { ...feed(item('v2.0.0')), target: 'win-x64' }, privateKey);
      expect(await readFile(path.join(dir, 'updates/desktop-v1/osx-arm64.json'))).toEqual(original);
      expect(await readFile(sentinel, 'utf8')).toBe('existing CLI index');
    } finally { await rm(dir, { recursive: true, force: true }); }
  });
});
