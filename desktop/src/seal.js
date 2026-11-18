'use strict';
// Fetch's end-to-end encryption for the desktop app's background process,
// on Node's own crypto. Implements spec/encryption.md and is tested against
// spec/vectors.json, like every other implementation.

const crypto = require('node:crypto');
const fs = require('node:fs');

const VERSION = 1;
const CHUNK = 65536;
const TAG = 16;
const SALT = 16;
const META_OVERHEAD = 1 + SALT + TAG;
const MAX_TEXT_IN_META = 256 * 1024;
const SEALED_TYPE = 'application/x-fetch-sealed';

class OpenError extends Error {
  constructor(message) {
    super(message || "Can't decrypt: this was made with a different key, or it was changed.");
  }
}

const b64url = (buf) => Buffer.from(buf).toString('base64url');

function newKey() { return crypto.randomBytes(32); }
function keyToText(key) { return b64url(key); }
function keyFromText(text) {
  const s = String(text || '').trim().replace(/=+$/, '');
  const k = /^[A-Za-z0-9_-]{43}$/.test(s) ? Buffer.from(s, 'base64url') : null;
  if (!k || k.length !== 32) throw new Error("That isn't a Fetch key.");
  return k;
}

function hkdf(key, salt, info, n) {
  return Buffer.from(crypto.hkdfSync('sha256', key, salt || Buffer.alloc(32), Buffer.from(info), n));
}

function check(key) { return hkdf(key, null, 'fetch v1 check', 16).toString('hex'); }
function fingerprint(key) {
  const c = check(key).slice(0, 8).toUpperCase();
  return c.slice(0, 4) + '-' + c.slice(4);
}

function itemKeys(key, salt) {
  return { meta: hkdf(key, salt, 'fetch v1 meta', 32), data: hkdf(key, salt, 'fetch v1 data', 32) };
}

function gcmSeal(key, nonce, plain) {
  const c = crypto.createCipheriv('aes-256-gcm', key, nonce);
  return Buffer.concat([c.update(plain), c.final(), c.getAuthTag()]);
}

function gcmOpen(key, nonce, sealed) {
  if (sealed.length < TAG) throw new OpenError();
  try {
    const d = crypto.createDecipheriv('aes-256-gcm', key, nonce);
    d.setAuthTag(sealed.subarray(sealed.length - TAG));
    return Buffer.concat([d.update(sealed.subarray(0, sealed.length - TAG)), d.final()]);
  } catch {
    throw new OpenError();
  }
}

// ---- pairing links ----

// http for an address on a home network, https for a domain.
function normalizeURL(input) {
  let u = String(input || '').trim().replace(/\/+$/, '');
  if (!u) return '';
  if (!u.includes('://')) {
    const host = u.split('/')[0];
    const local = host.includes(':') || !host.includes('.') || /\.(local|lan)$/.test(host) || /^[0-9.]+$/.test(host);
    u = (local ? 'http://' : 'https://') + u;
  }
  try {
    const parsed = new URL(u);
    if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') return '';
    return parsed.origin;
  } catch {
    return '';
  }
}

function pairingLink({ hub, token, key }) {
  return `fetchpair://v1?hub=${encodeURIComponent(hub)}&key=${keyToText(key)}&token=${encodeURIComponent(token)}`;
}

function parsePairing(link) {
  const m = /^fetchpair:\/\/v1\?(.*)$/i.exec(String(link || '').trim());
  if (!m) throw new Error("That isn't a Fetch pairing link (they start with fetchpair://).");
  const q = new URLSearchParams(m[1]);
  const hub = normalizeURL(q.get('hub'));
  const token = (q.get('token') || '').trim();
  let key = null;
  try { key = keyFromText(q.get('key')); } catch { /* reported below */ }
  if (!hub || !token || !key) throw new Error('That pairing link is incomplete. Copy it again.');
  return { hub, token, key };
}

// An invite link: the hub's address with the code after #, as in
// https://fetch.example.com/#invite=abcd-efgh-jkmn. Returns { hub, code }, or
// null for anything else.
function parseInvite(link) {
  const m = /^(https?:\/\/[^#]+)#(.*)$/i.exec(String(link || '').trim());
  if (!m) return null;
  const code = (new URLSearchParams(m[2]).get('invite') || '').trim();
  const hub = normalizeURL(m[1].split('?')[0]);
  return hub && code ? { hub, code } : null;
}

// ---- metadata ----

function sealMetaWithSalt(key, meta, salt) {
  const m = { v: VERSION, kind: meta.kind };
  for (const f of ['device', 'text', 'name', 'mime']) if (meta[f]) m[f] = String(meta[f]);
  if (meta.size) m.size = meta.size;
  const ct = gcmSeal(itemKeys(key, salt).meta, Buffer.alloc(12), Buffer.from(JSON.stringify(m)));
  return Buffer.concat([Buffer.from([VERSION]), salt, ct]);
}

function sealMeta(key, meta) {
  const salt = crypto.randomBytes(SALT);
  return { envelope: sealMetaWithSalt(key, meta, salt), salt };
}

function openMeta(key, envelope) {
  const env = Buffer.from(envelope);
  if (env.length < META_OVERHEAD || env[0] !== VERSION) throw new OpenError();
  const salt = env.subarray(1, 1 + SALT);
  const plain = gcmOpen(itemKeys(key, salt).meta, Buffer.alloc(12), env.subarray(1 + SALT));
  let meta;
  try { meta = JSON.parse(plain.toString('utf8')); } catch { throw new OpenError(); }
  if (!meta || typeof meta !== 'object') throw new OpenError();
  return { meta, salt: Buffer.from(salt) };
}

// ---- data ----

function nonce(i, last) {
  const n = Buffer.alloc(12);
  n.writeBigUInt64BE(BigInt(i), 3);
  n[11] = last ? 1 : 0;
  return n;
}

function sealedSize(plain) {
  return plain + Math.max(1, Math.ceil(plain / CHUNK)) * TAG;
}

// Encrypted chunks of a Buffer.
function* sealBuffer(key, salt, plain) {
  const k = itemKeys(key, salt).data;
  const n = Math.max(1, Math.ceil(plain.length / CHUNK));
  for (let i = 0; i < n; i++) yield gcmSeal(k, nonce(i, i === n - 1), plain.subarray(i * CHUNK, (i + 1) * CHUNK));
}

// Encrypted chunks of a file on disk, read one chunk at a time.
async function* sealFile(key, salt, path) {
  const k = itemKeys(key, salt).data;
  const fh = await fs.promises.open(path, 'r');
  try {
    const size = (await fh.stat()).size;
    const n = Math.max(1, Math.ceil(size / CHUNK));
    const buf = Buffer.alloc(CHUNK);
    for (let i = 0; i < n; i++) {
      const want = Math.min(CHUNK, size - i * CHUNK);
      let got = 0;
      while (got < want) {
        const { bytesRead } = await fh.read(buf, got, want - got, i * CHUNK + got);
        if (bytesRead === 0) throw new Error('The file changed while it was being sent.');
        got += bytesRead;
      }
      yield gcmSeal(k, nonce(i, i === n - 1), buf.subarray(0, want));
    }
  } finally {
    await fh.close();
  }
}

// Decrypts a whole encrypted data stream held in memory.
function openBuffer(key, salt, sealed) {
  const k = itemKeys(key, salt).data;
  const data = Buffer.from(sealed);
  if (data.length < TAG) throw new OpenError("Can't decrypt: the data was cut short.");
  const out = [];
  let at = 0, i = 0;
  for (;;) {
    const rest = data.length - at;
    const last = rest <= CHUNK + TAG;
    const size = last ? rest : CHUNK + TAG;
    out.push(gcmOpen(k, nonce(i++, last), data.subarray(at, at + size)));
    at += size;
    if (last) return Buffer.concat(out);
  }
}

function uploadHead(envelope) {
  const head = Buffer.alloc(4);
  head.writeUInt32BE(envelope.length);
  return Buffer.concat([head, envelope]);
}

module.exports = {
  VERSION, CHUNK, MAX_TEXT_IN_META, SEALED_TYPE, OpenError,
  newKey, keyToText, keyFromText, check, fingerprint, normalizeURL, pairingLink, parsePairing, parseInvite,
  sealMeta, sealMetaWithSalt, openMeta, sealBuffer, sealFile, openBuffer, sealedSize, uploadHead,
};
