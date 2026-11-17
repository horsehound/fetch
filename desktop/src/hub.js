'use strict';
// Talks to a Fetch hub. No Electron imports, so this file is unit-testable.

const fs = require('node:fs');
const path = require('node:path');

class HubError extends Error {}

async function request(cfg, route, init = {}) {
  let res;
  try {
    res = await fetch(cfg.url + route, {
      ...init,
      headers: {
        Authorization: `Bearer ${cfg.token}`,
        'X-Device': encodeURIComponent(cfg.device || ''),
        ...init.headers,
      },
    });
  } catch (err) {
    if (err.name === 'TimeoutError') throw new HubError(`The hub at ${cfg.url} didn't answer.`);
    throw new HubError(`Can't reach the hub at ${cfg.url}.`);
  }
  if (res.status === 401) throw new HubError('The hub rejected the token.');
  if (!res.ok) {
    const body = (await res.text().catch(() => '')).trim().slice(0, 200);
    throw new HubError(body || `The hub answered ${res.status}.`);
  }
  return res;
}

// Confirms the address and token work. Used before saving settings.
async function check(cfg) {
  const res = await request(cfg, '/api/info', { signal: AbortSignal.timeout(8000) });
  try {
    return await res.json();
  } catch {
    throw new HubError(`${cfg.url} answered, but it isn't a Fetch hub.`);
  }
}

function sendText(cfg, text) {
  return request(cfg, '/api/items', {
    method: 'POST',
    headers: { 'Content-Type': 'text/plain; charset=utf-8' },
    body: text,
  });
}

// A file is either a path on disk (copied from the file manager) or bytes
// (a picture pasted from the clipboard).
async function sendFile(cfg, file) {
  let body, name = file.name || '';
  if (file.path) {
    const stat = await fs.promises.stat(file.path).catch(() => null);
    if (!stat) throw new HubError(`${path.basename(file.path)} isn't there any more.`);
    if (stat.isDirectory()) throw new HubError(`${path.basename(file.path)} is a folder; only files can be thrown.`);
    body = await fs.openAsBlob(file.path); // streamed from disk, not read into memory
    name = path.basename(file.path);
  } else {
    body = new Blob([file.bytes]);
    // Every clipboard picture arrives as "image.png"; let the hub give it a dated name.
    if (name === 'image.png') name = '';
  }
  const query = name ? `?name=${encodeURIComponent(name)}` : '';
  return request(cfg, '/api/items' + query, {
    method: 'POST',
    headers: { 'Content-Type': file.type || 'application/octet-stream' },
    body,
  });
}

// Sends one "throw": optional text plus any number of files, in order.
async function send(cfg, { text, files }) {
  if (text && text.trim()) await sendText(cfg, text);
  for (const file of files || []) await sendFile(cfg, file);
}

module.exports = { HubError, check, send, sendText, sendFile };
