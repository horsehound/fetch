'use strict';
// The web page's encryption must read and write exactly what the reference
// implementation (hub/seal.go) does, recorded in spec/vectors.json.
const assert = require('node:assert/strict');
const { test } = require('node:test');
const path = require('node:path');
require('../web/vendor.js');
const C = require('../web/fetch-crypto.js');
const V = require(path.join(__dirname, '../../spec/vectors.json'));

const pattern = (n) => Uint8Array.from({ length: n }, (_, i) => (i * 7 + 3) & 255);
const hex = (s) => Uint8Array.from(s.match(/../g) || [], (h) => parseInt(h, 16));
const b64 = (s) => (s ? new Uint8Array(Buffer.from(s, 'base64')) : new Uint8Array(0));
const key = C.keyFromText(V.key);

function openAll(salt, data, splitAt) {
  const opener = C.dataOpener(key, salt);
  const out = [];
  for (let at = 0; at < data.length; at += splitAt) out.push(...opener.push(data.subarray(at, at + splitAt)));
  out.push(...opener.end());
  return Buffer.concat(out);
}

function sealAll(salt, plain) {
  const s = C.dataSealer(key, salt);
  const n = Math.max(1, Math.ceil(plain.length / C.CHUNK));
  const out = [];
  for (let c = 0; c < n; c++) out.push(s.seal(plain.subarray(c * C.CHUNK, (c + 1) * C.CHUNK), c === n - 1));
  return Buffer.concat(out);
}

test('key, check and pairing link', () => {
  assert.equal(Buffer.from(key).toString('hex'), V.key_hex);
  assert.equal(C.keyToText(key), V.key);
  assert.equal(C.check(key), V.check);
  assert.equal(C.pairingLink({ hub: V.hub, token: V.token, key }), V.link);
  const p = C.parsePairing('  ' + V.link + '\n');
  assert.equal(p.hub, V.hub);
  assert.equal(p.token, V.token);
  assert.deepEqual(p.key, key);
  assert.equal(C.fingerprint(key), V.check.slice(0, 4).toUpperCase() + '-' + V.check.slice(4, 8).toUpperCase());
});

test('every envelope opens to its metadata', () => {
  for (const it of V.items) {
    const { meta, salt } = C.openMeta(key, b64(it.envelope));
    assert.deepEqual(meta, JSON.parse(JSON.stringify(it.meta)), it.name);
    assert.equal(Buffer.from(salt).toString('hex'), it.salt, it.name);
  }
});

test('envelopes written here open the same way', () => {
  for (const it of V.items) {
    const env = C._sealMetaWithSalt(key, it.meta, hex(it.salt));
    // JSON may be spelled differently, so compare what opens, not the bytes.
    assert.deepEqual(C.openMeta(key, env).meta, it.meta, it.name);
    assert.equal(env.length, b64(it.envelope).length, it.name);
  }
});

test('data decrypts, however it arrives', () => {
  for (const it of V.items.filter((i) => i.data)) {
    for (const split of [1 << 30, 65553, 1000, 7]) {
      if (split === 7 && it.data_size > 2000) continue;
      assert.deepEqual(openAll(hex(it.salt), b64(it.data), split), Buffer.from(pattern(it.data_size)), `${it.name} in pieces of ${split}`);
    }
  }
});

test('data encrypts to exactly the reference bytes', () => {
  for (const it of V.items.filter((i) => i.data)) {
    const sealed = sealAll(hex(it.salt), pattern(it.data_size));
    assert.equal(sealed.toString('base64'), it.data, it.name);
    assert.equal(sealed.length, C.sealedSize(it.data_size), it.name);
  }
});

test('altered items do not open', () => {
  for (const c of V.corrupt) {
    const it = V.items[c.of];
    let env = b64(it.envelope), data = b64(it.data);
    if (c.op === 'flip_envelope') { env = env.slice(); env[env.length - 1] ^= 1; }
    else if (c.op.startsWith('keep:')) data = data.slice(0, Number(c.op.slice(5)));
    else if (c.op === 'append_zero') data = Buffer.concat([data, Buffer.from([0])]);
    else if (c.op === 'swap_first_chunks') data = Buffer.concat([data.subarray(65552, 131104), data.subarray(0, 65552), data.subarray(131104)]);
    const metaFails = (() => { try { C.openMeta(key, env); return false; } catch (e) { return e instanceof C.OpenError; } })();
    const dataFails = !it.data || (() => { try { openAll(hex(it.salt), new Uint8Array(data), 1 << 30); return false; } catch (e) { return e instanceof C.OpenError; } })();
    assert.ok(metaFails || (it.data && dataFails), c.name);
  }
  const other = C.newKey();
  assert.throws(() => C.openMeta(other, b64(V.items[0].envelope)), C.OpenError);
});

test('round trip with a fresh key, through Blobs', async () => {
  const k = C.newKey();
  const { envelope, salt } = C.sealMeta(k, { kind: 'file', name: 'grüße.bin', device: 'Test' });
  const plain = pattern(3 * C.CHUNK + 5);
  const chunks = await C.sealBlob(k, salt, new Blob([plain]));
  const body = Buffer.concat(C.uploadParts(envelope, chunks));
  const n = body.readUInt32BE(0);
  const opened = C.openMeta(k, new Uint8Array(body.subarray(4, 4 + n)));
  assert.equal(opened.meta.name, 'grüße.bin');
  const opener = C.dataOpener(k, opened.salt);
  const out = [...opener.push(new Uint8Array(body.subarray(4 + n))), ...opener.end()];
  assert.deepEqual(Buffer.concat(out), Buffer.from(plain));
  const empty = await C.sealBlob(k, salt, new Blob([]));
  assert.equal(empty.length, 1);
  assert.equal(empty[0].length, 16);
});

test('pairing links are checked', () => {
  for (const bad of ['', 'https://example.com', 'fetchpair://v2?hub=a&token=b&key=' + V.key,
    'fetchpair://v1?hub=a&token=b&key=short', 'fetchpair://v1?token=b&key=' + V.key, 'fetchpair://v1?hub=ftp%3A%2F%2Fx&token=b&key=' + V.key]) {
    assert.throws(() => C.parsePairing(bad), Error, bad);
  }
  assert.equal(C.parsePairing('fetchpair://v1?hub=192.168.1.20%3A8787&token=t&key=' + V.key).hub, 'http://192.168.1.20:8787');
  assert.equal(C.parsePairing('fetchpair://v1?hub=fetch.example.com&token=t&key=' + V.key).hub, 'https://fetch.example.com');
});

test('invite links', () => {
  for (const inv of V.invites) assert.deepEqual(C.parseInvite(inv.link), { hub: inv.hub, code: inv.code }, inv.link);
  for (const s of V.not_invites) assert.equal(C.parseInvite(s), null, s);
  assert.equal(C.inviteLink('https://fetch.example.com/', 'abcd-efgh-jkmn'), V.invites[0].link);
});
