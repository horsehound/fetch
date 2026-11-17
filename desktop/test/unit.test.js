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

test('the scheme is guessed the same way as in the hub client and the iPhone app', () => {
  const cases = {
    '192.168.1.20:8787': 'http://192.168.1.20:8787',
    '10.0.0.5': 'http://10.0.0.5',
    'proxmox:8787/': 'http://proxmox:8787',
    'hub.lan': 'http://hub.lan',
    'fetch.example.com': 'https://fetch.example.com',
    'fetch.example.com/': 'https://fetch.example.com',
    'http://fetch.example.com': 'http://fetch.example.com',
    'https://192.168.1.20:8787/': 'https://192.168.1.20:8787',
    '  ': '',
    'ftp://example.com': '',
    'javascript:alert(1)': '',
  };
  for (const [input, want] of Object.entries(cases)) {
    assert.equal(config.normalizeURL(input), want, input);
  }
});

test('settings survive a restart, ignore junk, and stay private', () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'fetch-cfg-'));
  const file = path.join(dir, 'nested', 'config.json');
  const store = config.open(file);
  assert.equal(config.isConfigured(store.get()), false);
  assert.equal(store.get().hotkey, config.DEFAULT_HOTKEY);

  store.set({ url: 'hub.lan:8787', token: '  secret-token-123  ', device: 'x'.repeat(80), hotkey: '', bogus: 1 });
  const reopened = config.open(file).get();
  assert.deepEqual(reopened, {
    url: 'http://hub.lan:8787',
    token: 'secret-token-123',
    device: 'x'.repeat(40),
    hotkey: '', // an empty shortcut is a choice, not a missing value
    openAtLogin: false,
  });
  assert.equal(config.isConfigured(reopened), true);
  if (process.platform !== 'win32') assert.equal(fs.statSync(file).mode & 0o077, 0);

  fs.writeFileSync(file, '{ not json');
  assert.equal(config.isConfigured(config.open(file).get()), false, 'a corrupt file falls back to defaults');
  fs.rmSync(dir, { recursive: true });
});

// ---- against a real hub ----
const HUB_BIN = process.env.FETCH_HUB_BIN;
const skip = HUB_BIN ? false : 'set FETCH_HUB_BIN to run';
const port = 19000 + Math.floor(Math.random() * 1000);
const cfg = { url: `http://127.0.0.1:${port}`, token: 'unit-token-0123456789', device: 'Ünit rig' };
let proc, tmp;

before(async () => {
  if (!HUB_BIN) return;
  tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'fetch-unit-'));
  proc = spawn(HUB_BIN, ['serve', '-addr', `127.0.0.1:${port}`, '-data', path.join(tmp, 'hub'), '-token', cfg.token, '-max-mb', '1'], { stdio: 'ignore' });
  for (let i = 0; i < 50; i++) {
    if (await fetch(cfg.url).then((r) => r.ok, () => false)) return;
    await new Promise((r) => setTimeout(r, 100));
  }
  throw new Error('hub did not start');
});
after(() => {
  if (proc) proc.kill();
  if (tmp) fs.rmSync(tmp, { recursive: true, force: true });
});

const items = async () => (await (await fetch(cfg.url + '/api/items', { headers: { Authorization: `Bearer ${cfg.token}` } })).json()).items;

test('a throw sends text, clipboard bytes and files from disk, in order', { skip }, async () => {
  const onDisk = path.join(tmp, 'report final.pdf');
  fs.writeFileSync(onDisk, '%PDF-1.4 ' + 'x'.repeat(5000));
  await hub.send(cfg, {
    text: 'sudo pacman -Syu',
    files: [
      { name: 'image.png', type: 'image/png', bytes: Uint8Array.from([0x89, 0x50, 0x4e, 0x47, 1, 2, 3]).buffer },
      { path: onDisk },
    ],
  });
  const [pdf, png, text] = await items(); // newest first
  assert.equal(text.text, 'sudo pacman -Syu');
  assert.equal(text.device, 'Ünit rig', 'non-ASCII device names survive the header');
  assert.equal(text.lang, 'bash', 'the hub recognises the command as shell');
  assert.equal(png.kind, 'image');
  assert.match(png.name, /^image-\d{8}-\d{6}\.png$/);
  assert.equal(png.size, 7);
  assert.equal(pdf.name, 'report final.pdf');
  assert.equal(pdf.mime, 'application/pdf');
  assert.equal(pdf.size, 5009);
});

test('whitespace-only text is not sent', { skip }, async () => {
  const before = (await items()).length;
  await hub.send(cfg, { text: '  \n ', files: [] });
  assert.equal((await items()).length, before);
});

test('failures come back as messages a person can act on', { skip }, async () => {
  await assert.rejects(hub.check({ ...cfg, token: 'wrong-token-00000' }), /rejected the token/);
  await assert.rejects(hub.check({ ...cfg, url: 'http://127.0.0.1:1' }), /Can't reach the hub at http:\/\/127\.0\.0\.1:1/);
  await assert.rejects(hub.send(cfg, { text: '', files: [{ path: path.join(tmp, 'gone.txt') }] }), /gone\.txt isn't there any more/);
  await assert.rejects(hub.send(cfg, { text: '', files: [{ path: tmp }] }), /is a folder/);
  const big = path.join(tmp, 'big.bin');
  fs.writeFileSync(big, Buffer.alloc((1 << 20) + 1));
  await assert.rejects(hub.send(cfg, { text: '', files: [{ path: big }] }), /too large: the limit is 1 MB/);
  assert.ok(await hub.check(cfg), 'and the right details pass');
});
