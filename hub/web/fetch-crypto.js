/*
 * Fetch's end-to-end encryption for the web page and the desktop app.
 * Implements spec/encryption.md and is tested against spec/vectors.json.
 * Needs vendor.js (FetchVendor: AES-GCM, HKDF, SHA-256 from @noble) loaded
 * first, so it works on plain-http pages where crypto.subtle is unavailable.
 */
(function (root) {
  'use strict';
  var V = root.FetchVendor;
  if (!V) throw new Error('vendor.js has to be loaded before fetch-crypto.js');

  var VERSION = 1;
  var CHUNK = 65536;
  var TAG = 16;
  var SEALED_CHUNK = CHUNK + TAG;
  var SALT = 16;
  var META_OVERHEAD = 1 + SALT + TAG;
  var MAX_TEXT_IN_META = 256 * 1024; // longer text is sent as a .txt file
  var SEALED_TYPE = 'application/x-fetch-sealed';

  var enc = new TextEncoder();
  var dec = new TextDecoder('utf-8', { fatal: true });

  function OpenError(message) {
    this.name = 'OpenError';
    this.message = message || "Can't decrypt: this was made with a different key, or it was changed.";
  }
  OpenError.prototype = Object.create(Error.prototype);

  function random(n) {
    var b = new Uint8Array(n);
    root.crypto.getRandomValues(b);
    return b;
  }

  function concat(parts) {
    var n = 0;
    parts.forEach(function (p) { n += p.length; });
    var out = new Uint8Array(n);
    var at = 0;
    parts.forEach(function (p) { out.set(p, at); at += p.length; });
    return out;
  }

  // ---- text forms ----

  function toBase64(bytes) {
    var s = '';
    for (var i = 0; i < bytes.length; i += 0x8000) s += String.fromCharCode.apply(null, bytes.subarray(i, i + 0x8000));
    return btoa(s);
  }
  function fromBase64(text) {
    var s = atob(text);
    var out = new Uint8Array(s.length);
    for (var i = 0; i < s.length; i++) out[i] = s.charCodeAt(i);
    return out;
  }
  function toBase64Url(bytes) { return toBase64(bytes).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, ''); }
  function fromBase64Url(text) {
    var s = text.replace(/-/g, '+').replace(/_/g, '/').replace(/=+$/, '');
    while (s.length % 4) s += '=';
    return fromBase64(s);
  }
  function toHex(bytes) {
    var s = '';
    for (var i = 0; i < bytes.length; i++) s += (bytes[i] < 16 ? '0' : '') + bytes[i].toString(16);
    return s;
  }

  // ---- the key ----

  function newKey() { return random(32); }

  function keyToText(key) { return toBase64Url(key); }

  function keyFromText(text) {
    var k;
    try { k = fromBase64Url(String(text || '').trim()); } catch (e) { k = null; }
    if (!k || k.length !== 32) throw new Error("That isn't a Fetch key.");
    return k;
  }

  function hkdf(key, salt, info, n) {
    return V.hkdf(V.sha256, key, salt || new Uint8Array(32), enc.encode(info), n);
  }

  // Fingerprint of the key that the hub keeps; reveals nothing about the key.
  function check(key) { return toHex(hkdf(key, null, 'fetch v1 check', 16)); }

  // The short form people compare by eye: ABCD-EF01.
  function fingerprint(key) {
    var c = check(key).slice(0, 8).toUpperCase();
    return c.slice(0, 4) + '-' + c.slice(4);
  }

  function itemKeys(key, salt) {
    return { meta: hkdf(key, salt, 'fetch v1 meta', 32), data: hkdf(key, salt, 'fetch v1 data', 32) };
  }

  // ---- pairing links ----

  // http for an address on a home network, https for a domain. Every client
  // fills in a missing scheme the same way.
  function normalizeURL(input) {
    var u = String(input || '').trim().replace(/\/+$/, '');
    if (!u) return '';
    if (u.indexOf('://') < 0) {
      var host = u.split('/')[0];
      var local = host.indexOf(':') >= 0 || host.indexOf('.') < 0 || /\.(local|lan)$/.test(host) || /^[0-9.]+$/.test(host);
      u = (local ? 'http://' : 'https://') + u;
    }
    try {
      var parsed = new URL(u);
      if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') return '';
      return parsed.origin;
    } catch (e) {
      return '';
    }
  }

  function pairingLink(p) {
    var q = 'hub=' + encodeURIComponent(p.hub) + '&key=' + keyToText(p.key) + '&token=' + encodeURIComponent(p.token);
    return 'fetchpair://v1?' + q;
  }

  function parsePairing(link) {
    var s = String(link || '').trim();
    var m = /^fetchpair:\/\/v1\?(.*)$/i.exec(s);
    if (!m) throw new Error("That isn't a Fetch pairing link (they start with fetchpair://).");
    var q = {};
    m[1].split('&').forEach(function (pair) {
      var i = pair.indexOf('=');
      if (i < 0) return;
      try { q[decodeURIComponent(pair.slice(0, i))] = decodeURIComponent(pair.slice(i + 1).replace(/\+/g, ' ')); } catch (e) { /* skip */ }
    });
    var hub = normalizeURL(q.hub), token = (q.token || '').trim(), key;
    try { key = keyFromText(q.key); } catch (e) { key = null; }
    if (!hub || !token || !key) throw new Error('That pairing link is incomplete. Copy it again.');
    return { hub: hub, token: token, key: key };
  }

  // ---- metadata ----

  function sealMetaWithSalt(key, meta, salt) {
    var m = { v: VERSION, kind: meta.kind };
    ['device', 'text', 'name', 'mime'].forEach(function (f) { if (meta[f]) m[f] = String(meta[f]); });
    if (meta.size) m.size = meta.size;
    var nonce = new Uint8Array(12);
    var ct = V.gcm(itemKeys(key, salt).meta, nonce).encrypt(enc.encode(JSON.stringify(m)));
    return concat([new Uint8Array([VERSION]), salt, ct]);
  }

  function sealMeta(key, meta) {
    var salt = random(SALT);
    return { envelope: sealMetaWithSalt(key, meta, salt), salt: salt };
  }

  function openMeta(key, envelope) {
    if (!envelope || envelope.length < META_OVERHEAD || envelope[0] !== VERSION) throw new OpenError();
    var salt = envelope.slice(1, 1 + SALT);
    var plain;
    try {
      plain = V.gcm(itemKeys(key, salt).meta, new Uint8Array(12)).decrypt(envelope.subarray(1 + SALT));
    } catch (e) {
      throw new OpenError();
    }
    var meta;
    try { meta = JSON.parse(dec.decode(plain)); } catch (e) { throw new OpenError(); }
    if (!meta || typeof meta !== 'object') throw new OpenError();
    return { meta: meta, salt: salt };
  }

  // ---- data ----

  function nonce(i, last) {
    var n = new Uint8Array(12);
    // Chunk numbers stay far below 2^53, so plain arithmetic is exact.
    for (var b = 10, v = i; b >= 3 && v > 0; b--) { n[b] = v % 256; v = Math.floor(v / 256); }
    n[11] = last ? 1 : 0;
    return n;
  }

  function sealedSize(plain) {
    var chunks = Math.max(1, Math.ceil(plain / CHUNK));
    return plain + chunks * TAG;
  }

  // Encrypts chunk by chunk: call seal(chunk, isLast) with plaintext chunks of
  // exactly CHUNK bytes, except the last.
  function dataSealer(key, salt) {
    var k = itemKeys(key, salt).data;
    var i = 0;
    return {
      seal: function (chunk, last) {
        if (!last && chunk.length !== CHUNK) throw new Error('only the last chunk may be short');
        return V.gcm(k, nonce(i++, last)).encrypt(chunk);
      }
    };
  }

  // Decrypts a stream: push() what arrives, end() when it is complete. Both
  // return the plaintext pieces ready so far; end() throws if the data was
  // cut short, extended or altered.
  function dataOpener(key, salt) {
    var k = itemKeys(key, salt).data;
    var i = 0;
    var pending = new Uint8Array(0);
    var done = false;
    function open(chunk, last) {
      try {
        return V.gcm(k, nonce(i++, last)).decrypt(chunk);
      } catch (e) {
        throw new OpenError();
      }
    }
    return {
      push: function (bytes) {
        if (done) throw new OpenError();
        pending = pending.length ? concat([pending, bytes]) : bytes;
        var out = [];
        // A full chunk is only known not to be the last once more data follows it.
        while (pending.length > SEALED_CHUNK) {
          out.push(open(pending.subarray(0, SEALED_CHUNK), false));
          pending = pending.slice(SEALED_CHUNK);
        }
        return out;
      },
      end: function () {
        if (done) throw new OpenError();
        done = true;
        if (pending.length < TAG) throw new OpenError("Can't decrypt: the data was cut short.");
        return [open(pending, true)];
      }
    };
  }

  // Encrypts a Blob or File, reading it in chunks. Returns the encrypted chunks.
  async function sealBlob(key, salt, blob, onProgress) {
    var sealer = dataSealer(key, salt);
    var out = [];
    var n = Math.max(1, Math.ceil(blob.size / CHUNK));
    for (var c = 0; c < n; c++) {
      var piece = new Uint8Array(await blob.slice(c * CHUNK, (c + 1) * CHUNK).arrayBuffer());
      out.push(sealer.seal(piece, c === n - 1));
      if (onProgress) onProgress((c + 1) / n);
    }
    return out;
  }

  // The body of an upload: envelope length (4 bytes, big-endian), envelope, data.
  function uploadParts(envelope, chunks) {
    var head = new Uint8Array(4);
    new DataView(head.buffer).setUint32(0, envelope.length);
    return [head, envelope].concat(chunks || []);
  }

  var api = {
    VERSION: VERSION, CHUNK: CHUNK, SEALED_TYPE: SEALED_TYPE, MAX_TEXT_IN_META: MAX_TEXT_IN_META, OpenError: OpenError,
    newKey: newKey, keyToText: keyToText, keyFromText: keyFromText, check: check, fingerprint: fingerprint,
    normalizeURL: normalizeURL, pairingLink: pairingLink, parsePairing: parsePairing,
    sealMeta: sealMeta, openMeta: openMeta, dataSealer: dataSealer, dataOpener: dataOpener,
    sealBlob: sealBlob, uploadParts: uploadParts, sealedSize: sealedSize,
    toBase64: toBase64, fromBase64: fromBase64,
    _sealMetaWithSalt: sealMetaWithSalt // for the test vectors
  };
  root.FetchCrypto = api;
  if (typeof module !== 'undefined' && module.exports) module.exports = api;
})(typeof globalThis !== 'undefined' ? globalThis : this);
