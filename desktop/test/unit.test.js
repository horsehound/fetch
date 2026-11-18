'use strict';
// Unit tests for the parts that don't need a window. The hub tests need the
// hub binary: FETCH_HUB_BIN=/path/to/fetch (they are skipped without it).

const assert = require('node:assert/strict');
const { spawn } = require('node:child_process');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { test, before, after } = require('node:test');
const config = require('../src/config');
const hub = require('../src/hub');
const seal = require('../src/seal');
const V = require('../../spec/vectors.json');

const pattern = (n) => Buffer.from(Uint8Array.from({ length: n }, (_, i) => (i * 7 + 3) & 255));

// ---- the encryption format, against the reference vectors ----

test('key, check, fingerprint and pairing link match the reference', () => {
  const key = seal.keyFromText(V.key);
  assert.equal(key.toString('hex'), V.key_hex);
  assert.equal(seal.check(key), V.check);
  assert.equal(seal.pairingLink({ hub: V.hub, token: V.token, key }), V.link);
  const p = seal.parsePairing(V.link);
  assert.deepEqual([p.hub, p.token, p.key.toString('hex')], [V.hub, V.token, V.key_hex]);
  assert.throws(() => seal.keyFromText('short'));
  assert.throws(() => seal.parsePairing('https://example.com'));
});

test('reference envelopes open, and data opens and seals byte for byte', () => {
  const key = seal.keyFromText(V.key);
  for (const it of V.items) {
    const { meta, salt } = seal.openMeta(key, Buffer.from(it.envelope, 'base64'));
    assert.deepEqual(meta, it.meta, it.name);
    assert.equal(salt.toString('hex'), it.salt);
    assert.deepEqual(seal.openMeta(key, seal.sealMetaWithSalt(key, it.meta, salt)).meta, it.meta);
    if (!it.data) continue;
    const data = Buffer.from(it.data, 'base64');
    assert.deepEqual(seal.openBuffer(key, salt, data), pattern(it.data_size), it.name);
    assert.equal(Buffer.concat([...seal.sealBuffer(key, salt, pattern(it.data_size))]).toString('base64'), it.data, it.name);
  }
});

test('altered reference items do not open', () => {
  const key = seal.keyFromText(V.key);
  for (const c of V.corrupt) {
    const it = V.items[c.of];
    let env = Buffer.from(it.envelope, 'base64'), data = Buffer.from(it.data || '', 'base64');
    if (c.op === 'flip_envelope') env[env.length - 1] ^= 1;
    else if (c.op.startsWith('keep:')) data = data.subarray(0, Number(c.op.slice(5)));
    else if (c.op === 'append_zero') data = Buffer.concat([data, Buffer.from([0])]);
    else if (c.op === 'swap_first_chunks') data = Buffer.concat([data.subarray(65552, 131104), data.subarray(0, 65552), data.subarray(131104)]);
    const salt = Buffer.from(it.salt, 'hex');
    assert.throws(() => { seal.openMeta(key, env); if (it.data) seal.openBuffer(key, salt, data); }, seal.OpenError, c.name);
  }
});

test('a file streams through sealFile exactly like a buffer through sealBuffer', async () => {
  const key = seal.newKey(), salt = Buffer.alloc(16, 7);
  const file = path.join(fs.mkdtempSync(path.join(os.tmpdir(), 'fetch-seal-')), 'f.bin');
  for (const n of [0, 1, 65536, 65537, 200000]) {
    fs.writeFileSync(file, pattern(n));
    const chunks = [];
    for await (const c of seal.sealFile(key, salt, file)) chunks.push(c);
    const sealed = Buffer.concat(chunks);
    assert.deepEqual(sealed, Buffer.concat([...seal.sealBuffer(key, salt, pattern(n))]), `${n} bytes`);
    assert.equal(sealed.length, seal.sealedSize(n));
    assert.deepEqual(seal.openBuffer(key, salt, sealed), pattern(n));
  }
});

// ---- settings ----

test('settings survive a restart, ignore junk, and stay private', () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'fetch-cfg-'));
  const file = path.join(dir, 'nested', 'config.json');
  const store = config.open(file);
  assert.equal(config.isConfigured(store.get()), false);
  assert.equal(store.get().hotkey, config.DEFAULT_HOTKEY);

  store.set({ url: 'hub.lan:8787', token: '  secret-token-123  ', key: V.key, device: 'x'.repeat(80), hotkey: '', bogus: 1 });
  const reopened = config.open(file).get();
  assert.deepEqual(reopened, {
    url: 'http://hub.lan:8787',
    token: 'secret-token-123',
    key: V.key,
    device: 'x'.repeat(40),
    hotkey: '', // an empty shortcut is a choice, not a missing value
    openAtLogin: false,
  });
  assert.equal(config.isConfigured(reopened), true);
  assert.equal(config.isConfigured({ ...reopened, key: '' }), false, 'no key, no feed');
  assert.equal(config.clean({ key: 'not a key' }).key, '');
  if (process.platform !== 'win32') assert.equal(fs.statSync(file).mode & 0o077, 0);

  fs.writeFileSync(file, '{ not json');
  assert.equal(config.isConfigured(config.open(file).get()), false, 'a corrupt file falls back to defaults');
  fs.rmSync(dir, { recursive: true });
});

test('the scheme is guessed the same way as everywhere else', () => {
  const cases = {
    '192.168.1.20:8787': 'http://192.168.1.20:8787', 'proxmox:8787/': 'http://proxmox:8787', 'hub.lan': 'http://hub.lan',
    'fetch.example.com': 'https://fetch.example.com', 'https://192.168.1.20:8787/': 'https://192.168.1.20:8787',
    '  ': '', 'ftp://example.com': '', 'javascript:alert(1)': '',
  };
  for (const [input, want] of Object.entries(cases)) assert.equal(seal.normalizeURL(input), want, input);
});

// ---- against a real hub ----

const HUB_BIN = process.env.FETCH_HUB_BIN;
const skip = HUB_BIN ? false : 'set FETCH_HUB_BIN to run';
const port = 19000 + Math.floor(Math.random() * 1000);
const URL_ = `http://127.0.0.1:${port}`;
const TOKEN = 'unit-token-0123456789';
let proc, tmp, cfg;

before(async () => {
  if (!HUB_BIN) return;
  tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'fetch-unit-'));
  proc = spawn(HUB_BIN, ['serve', '-addr', `127.0.0.1:${port}`, '-data', path.join(tmp, 'hub'), '-token', TOKEN, '-max-mb', '1'], { stdio: 'ignore' });
  for (let i = 0; i < 50; i++) {
    if (await fetch(URL_).then((r) => r.ok, () => false)) return;
    await new Promise((r) => setTimeout(r, 100));
  }
  throw new Error('hub did not start');
});
after(() => {
  if (proc) proc.kill();
  if (tmp) fs.rmSync(tmp, { recursive: true, force: true });
});

const items = async () => (await (await fetch(URL_ + '/api/items', { headers: { Authorization: `Bearer ${TOKEN}` } })).json()).items;
const raw = async (id) => Buffer.from(await (await fetch(`${URL_}/api/items/${id}/raw`, { headers: { Authorization: `Bearer ${TOKEN}` } })).arrayBuffer());

test('setting up creates a key, and a second set-up is refused', { skip }, async () => {
  const creds = await hub.setUp(`127.0.0.1:${port}`, TOKEN);
  assert.equal(creds.url, URL_);
  cfg = { ...creds, device: 'Ünit rig' };
  await hub.verify(cfg);
  await assert.rejects(hub.setUp(URL_, TOKEN), (e) => e.canReset === true && /already has a key/.test(e.message));
  await assert.rejects(hub.setUp(URL_, 'wrong-token-00000'), /rejected the token/);
});

test('a pairing link joins; a stale one is refused', { skip }, async () => {
  const key = seal.keyFromText(cfg.key);
  const joined = await hub.pair(seal.pairingLink({ hub: URL_, token: TOKEN, key }));
  assert.equal(joined.key, cfg.key);
  await assert.rejects(hub.pair(seal.pairingLink({ hub: URL_, token: TOKEN, key: seal.newKey() })), /isn't the one the hub uses/);
  await assert.rejects(hub.pair('fetchpair://v1?hub=x'), /incomplete/);
});

test('a throw encrypts text, clipboard bytes and files from disk; the hub sees none of it', { skip }, async () => {
  const onDisk = path.join(tmp, 'report final.pdf');
  fs.writeFileSync(onDisk, pattern(150000));
  await hub.send(cfg, {
    text: 'sudo pacman -Syu',
    files: [
      { name: 'image.png', type: 'image/png', bytes: Uint8Array.from([0x89, 0x50, 0x4e, 0x47, 1, 2, 3]).buffer },
      { path: onDisk },
    ],
  });
  const key = seal.keyFromText(cfg.key);
  const [pdf, png, text] = await items(); // newest first
  const open = (it) => seal.openMeta(key, Buffer.from(it.meta, 'base64'));

  const t = open(text).meta;
  assert.deepEqual([t.kind, t.text, t.device], ['text', 'sudo pacman -Syu', 'Ünit rig']);
  assert.equal(text.size, 0);

  const p = open(png);
  assert.equal(p.meta.kind, 'image');
  assert.match(p.meta.name, /^image-\d{8}-\d{6}\.png$/);
  assert.deepEqual(seal.openBuffer(key, p.salt, await raw(png.id)), Buffer.from([0x89, 0x50, 0x4e, 0x47, 1, 2, 3]));

  const d = open(pdf);
  assert.deepEqual([d.meta.kind, d.meta.name, d.meta.mime, d.meta.size], ['file', 'report final.pdf', 'application/pdf', 150000]);
  const sealed = await raw(pdf.id);
  assert.equal(sealed.length, seal.sealedSize(150000));
  assert.ok(!sealed.includes(pattern(64)), 'the file is stored encrypted');
  assert.deepEqual(seal.openBuffer(key, d.salt, sealed), pattern(150000));

  const onHub = fs.readdirSync(path.join(tmp, 'hub', 'files')).map((f) => fs.readFileSync(path.join(tmp, 'hub', 'files', f)));
  const index = fs.readFileSync(path.join(tmp, 'hub', 'index.json'));
  for (const plain of ['pacman', 'report final', 'Ünit rig', 'application/pdf']) {
    assert.ok(!index.includes(plain) && !onHub.some((b) => b.includes(plain)), `${plain} is readable on the hub`);
  }
});

test('long text goes as a .txt file; whitespace-only text is not sent', { skip }, async () => {
  const before = (await items()).length;
  await hub.send(cfg, { text: '  \n ', files: [] });
  assert.equal((await items()).length, before);
  const long = 'é'.repeat(seal.MAX_TEXT_IN_META); // twice as many bytes as characters
  await hub.send(cfg, { text: long, files: [] });
  const [newest] = await items();
  const { meta, salt } = seal.openMeta(seal.keyFromText(cfg.key), Buffer.from(newest.meta, 'base64'));
  assert.equal(meta.kind, 'file');
  assert.match(meta.name, /^text-.*\.txt$/);
  assert.equal(seal.openBuffer(seal.keyFromText(cfg.key), salt, await raw(newest.id)).toString(), long);
});

test('failures come back as messages a person can act on', { skip }, async () => {
  await assert.rejects(hub.verify({ ...cfg, token: 'wrong-token-00000' }), /rejected the token/);
  await assert.rejects(hub.verify({ ...cfg, url: 'http://127.0.0.1:1' }), /Can't reach the hub at http:\/\/127\.0\.0\.1:1/);
  await assert.rejects(hub.verify({ ...cfg, key: seal.keyToText(seal.newKey()) }), /doesn't match/);
  await assert.rejects(hub.send(cfg, { text: '', files: [{ path: path.join(tmp, 'gone.txt') }] }), /gone\.txt isn't there any more/);
  await assert.rejects(hub.send(cfg, { text: '', files: [{ path: tmp }] }), /is a folder/);
  const big = path.join(tmp, 'big.bin');
  fs.writeFileSync(big, Buffer.alloc((1 << 20) + 1));
  await assert.rejects(hub.send(cfg, { text: '', files: [{ path: big }] }), /too large: the limit is 1 MB/);
});

// ------------------------------------------------------------ handing over

const version = require('../src/version');

test('isNewer compares release versions', () => {
  assert.equal(version.isNewer('1.0.21', '1.0.20'), true);
  assert.equal(version.isNewer('1.0.10', '1.0.9'), true);
  assert.equal(version.isNewer('1.1.0', '1.0.99'), true);
  assert.equal(version.isNewer('1.0.20', '1.0.20'), false);
  assert.equal(version.isNewer('1.0.9', '1.0.10'), false);
  assert.equal(version.isNewer(undefined, '1.0.0'), false);
});

test('cleanEnv drops the AppImage mount', () => {
  const env = version.cleanEnv({
    APPDIR: '/tmp/.mount_FetchAb', APPIMAGE: '/home/me/Fetch.AppImage', ARGV0: 'x', OWD: '/home/me',
    PATH: '/tmp/.mount_FetchAb/usr/bin:/usr/bin', LD_LIBRARY_PATH: '/tmp/.mount_FetchAb/usr/lib', HOME: '/home/me',
  });
  assert.deepEqual(env, { PATH: '/usr/bin', HOME: '/home/me' });
  assert.deepEqual(version.cleanEnv({ PATH: '/usr/bin' }), { PATH: '/usr/bin' });
});

test('ownArgs keeps only Fetch flags', () => {
  assert.deepEqual(version.ownArgs(['/opt/Fetch/fetch-desktop', '--enable-features=X', '--no-sandbox', '--throw']), ['--no-sandbox', '--throw']);
});
