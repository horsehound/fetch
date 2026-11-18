'use strict';
// Talks to a Fetch hub. Everything is encrypted here before it leaves; the hub
// only ever sees scrambled data. No Electron imports, so this file is
// unit-testable.

const fs = require('node:fs');
const path = require('node:path');
const { Readable } = require('node:stream');
const seal = require('./seal');

class HubError extends Error {}

// cfg: { url, token, key (text), device }
async function request(cfg, route, init = {}) {
  let res;
  try {
    res = await fetch(cfg.url + route, {
      ...init,
      headers: { Authorization: `Bearer ${cfg.token}`, ...init.headers },
    });
  } catch (err) {
    if (err.name === 'TimeoutError') throw new HubError(`The hub at ${cfg.url} didn't answer.`);
    throw new HubError(`Can't reach the hub at ${cfg.url}.`);
  }
  if (res.status === 401) throw Object.assign(new HubError('The hub rejected the token.'), { status: 401 });
  if (!res.ok) {
    const body = (await res.text().catch(() => '')).trim().slice(0, 200);
    throw Object.assign(new HubError(body || `The hub answered ${res.status}.`), { status: res.status });
  }
  return res;
}

// The fingerprint of the key the hub's devices share, or '' if no device has
// set one up yet.
async function hubCheck(cfg) {
  const res = await request(cfg, '/api/vault', { signal: AbortSignal.timeout(8000) });
  let v;
  try { v = await res.json(); } catch { v = null; }
  if (!v || typeof v.check !== 'string') throw new HubError(`${cfg.url} answered, but it isn't a Fetch hub with encryption. Update the hub.`);
  return v.check;
}

function claim(cfg, key) {
  return request(cfg, '/api/vault', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ check: seal.check(key) }),
  });
}

// Joins the hub with a pairing link, or makes a space of your own with an
// invite link. Returns { url, token, key } to save.
async function pair(link) {
  const invite = seal.parseInvite(link);
  if (invite) return join(invite.hub, invite.code);
  const p = seal.parsePairing(link);
  const cfg = { url: p.hub, token: p.token };
  const check = await hubCheck(cfg);
  if (check === '') await claim(cfg, p.key); // the hub was reset since the link was made
  else if (check !== seal.check(p.key)) throw new HubError("This link's key isn't the one the hub uses now. Make a fresh link on a device that works.");
  return { url: p.hub, token: p.token, key: seal.keyToText(p.key) };
}

// Makes a space on a hub (with an invite code, or none on a public hub) and
// sets this computer up as its first device: the hub hands out a token, the
// key is made here.
async function join(url, code) {
  url = seal.normalizeURL(url);
  if (!url) throw new HubError("That address doesn't look right.");
  let res;
  try {
    res = await fetch(url + '/api/spaces', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ invite: code || '' }),
      signal: AbortSignal.timeout(8000),
    });
  } catch {
    throw new HubError(`Can't reach the hub at ${url}.`);
  }
  if (res.status === 404 || res.status === 405) throw new HubError(`The hub at ${url} doesn't hand out spaces. It may need an update.`);
  if (!res.ok) {
    const message = (await res.text().catch(() => '')).trim().slice(0, 200) || `The hub answered ${res.status}.`;
    throw Object.assign(new HubError(message), { status: res.status });
  }
  const { token } = await res.json();
  const key = seal.newKey();
  await claim({ url, token }, key);
  return { url, token, key: seal.keyToText(key) };
}

// Sets up the first device of a feed, creating the key. The code is the
// hub's token (your own hub) or an invite code (someone else's); a public
// hub needs neither. With reset, a feed that already has a key is wiped first.
async function setUp(url, code, { reset = false } = {}) {
  const cfg = { url: seal.normalizeURL(url), token: String(code || '').trim() };
  if (!cfg.url) throw new HubError("That address doesn't look right.");
  if (!cfg.token) return join(cfg.url, '');
  if (reset) {
    await request(cfg, '/api/vault', { method: 'DELETE' });
  } else {
    let check;
    try {
      check = await hubCheck(cfg);
    } catch (err) {
      if (err.status !== 401) throw err;
      // Not a token here: maybe an invite code.
      try {
        return await join(cfg.url, cfg.token);
      } catch (joinErr) {
        if (joinErr.status === 403) throw new HubError("The hub didn't accept that as its token or as an invite code.");
        throw joinErr;
      }
    }
    if (check !== '') {
      const err = new HubError('This hub already has a key. Pair this computer from a device that has it, or start over.');
      err.canReset = true;
      throw err;
    }
  }
  const key = seal.newKey();
  await claim(cfg, key);
  return { url: cfg.url, token: cfg.token, key: seal.keyToText(key) };
}

// Confirms the saved key is still the hub's.
async function verify(cfg) {
  const check = await hubCheck(cfg);
  if (check !== seal.check(seal.keyFromText(cfg.key))) {
    throw new HubError(check === '' ? 'The hub was reset and has no key now.' : "This computer's key doesn't match the hub's anymore.");
  }
}

const INLINE_IMAGE = new Set(['image/png', 'image/jpeg', 'image/gif', 'image/webp', 'image/avif', 'image/bmp']);
const TYPES = {
  png: 'image/png', jpg: 'image/jpeg', jpeg: 'image/jpeg', gif: 'image/gif', webp: 'image/webp', avif: 'image/avif', bmp: 'image/bmp',
  heic: 'image/heic', svg: 'image/svg+xml', pdf: 'application/pdf', zip: 'application/zip', txt: 'text/plain', md: 'text/markdown',
  json: 'application/json', mp4: 'video/mp4', mov: 'video/quicktime', mp3: 'audio/mpeg', html: 'text/html', csv: 'text/csv',
};
const EXT = { 'image/png': '.png', 'image/jpeg': '.jpg', 'image/gif': '.gif', 'image/webp': '.webp', 'image/avif': '.avif', 'image/bmp': '.bmp', 'text/plain': '.txt' };

function stamp() {
  const d = new Date(), p = (n) => String(n).padStart(2, '0');
  return `${d.getFullYear()}${p(d.getMonth() + 1)}${p(d.getDate())}-${p(d.getHours())}${p(d.getMinutes())}${p(d.getSeconds())}`;
}

// Encrypts one item and uploads it. data is null (text), a Buffer, or a path.
async function upload(cfg, meta, data) {
  const key = seal.keyFromText(cfg.key);
  const { envelope, salt } = seal.sealMeta(key, { ...meta, device: cfg.device });
  async function* body() {
    yield seal.uploadHead(envelope);
    if (Buffer.isBuffer(data)) yield* seal.sealBuffer(key, salt, data);
    else if (typeof data === 'string') yield* seal.sealFile(key, salt, data);
  }
  return request(cfg, '/api/items', {
    method: 'POST',
    headers: { 'Content-Type': seal.SEALED_TYPE },
    body: Readable.from(body()),
    duplex: 'half',
  });
}

function sendText(cfg, text) {
  if (Buffer.byteLength(text) > seal.MAX_TEXT_IN_META) {
    const buf = Buffer.from(text);
    return upload(cfg, { kind: 'file', name: `text-${stamp()}.txt`, mime: 'text/plain', size: buf.length }, buf);
  }
  return upload(cfg, { kind: 'text', text }, null);
}

// A file is either a path on disk (copied from the file manager) or bytes
// (a picture pasted from the clipboard).
async function sendFile(cfg, file) {
  if (file.path) {
    const stat = await fs.promises.stat(file.path).catch(() => null);
    if (!stat) throw new HubError(`${path.basename(file.path)} isn't there any more.`);
    if (stat.isDirectory()) throw new HubError(`${path.basename(file.path)} is a folder; only files can be thrown.`);
    const name = path.basename(file.path);
    const mime = TYPES[path.extname(name).slice(1).toLowerCase()] || 'application/octet-stream';
    const kind = INLINE_IMAGE.has(mime) ? 'image' : 'file';
    return upload(cfg, { kind, name, mime, size: stat.size }, file.path);
  }
  const bytes = Buffer.from(file.bytes);
  const mime = file.type || 'application/octet-stream';
  const kind = INLINE_IMAGE.has(mime) ? 'image' : 'file';
  // Every clipboard picture arrives as "image.png"; give it a dated name.
  const name = file.name && file.name !== 'image.png' ? file.name : `${kind}-${stamp()}${EXT[mime] || ''}`;
  return upload(cfg, { kind, name, mime, size: bytes.length }, bytes);
}

// Sends one "throw": optional text plus any number of files, in order.
async function send(cfg, { text, files }) {
  if (text && text.trim()) await sendText(cfg, text);
  for (const file of files || []) await sendFile(cfg, file);
}

module.exports = { HubError, hubCheck, pair, join, setUp, verify, send, sendText, sendFile };
