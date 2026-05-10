#!/usr/bin/env node

import { readFile, writeFile } from 'node:fs/promises';
import path from 'node:path';

const root = path.resolve(import.meta.dirname, '..');
const markerStart = '# BEGIN NETSGO COMMON UPDATE HELPERS';
const markerEnd = '# END NETSGO COMMON UPDATE HELPERS';

async function commonHelpers() {
  const raw = await readFile(path.join(root, 'scripts/common-update.sh'), 'utf8');
  const lines = raw.split(/\r?\n/);
  const body = lines.slice(4).join('\n').trimEnd(); // drop shebang, blank line and set -eu
  return `${markerStart}\n${body}\n${markerEnd}`;
}

function replaceBlock(template, helpers) {
  const start = template.indexOf(markerStart);
  const end = template.indexOf(markerEnd);
  if (start === -1 || end === -1 || end < start) {
    throw new Error('standalone helper marker block is missing');
  }
  return `${template.slice(0, start)}${helpers}${template.slice(end + markerEnd.length)}`;
}

async function updateScript(relativePath, helpers) {
  const file = path.join(root, relativePath);
  const raw = await readFile(file, 'utf8');
  await writeFile(file, replaceBlock(raw, helpers));
}

const helpers = await commonHelpers();
await updateScript('scripts/install.sh', helpers);
await updateScript('scripts/upgrade.sh', helpers);
