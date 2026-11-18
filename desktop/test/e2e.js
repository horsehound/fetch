'use strict';
// End-to-end check of the desktop app against a real hub.
//
//   go build -o /tmp/fetch ./hub          (from the repo root)
//   FETCH_HUB_BIN=/tmp/fetch xvfb-run -a node test/e2e.js
//
// Starts a hub, starts the app with a throwaway profile, and drives it the way
// a person would: hotkey command, paste, Throw, full window, pairing.
// Everything on the hub is checked to be encrypted.

const assert = require('node:assert/strict');
const { spawn, spawnSync } = require('node:child_process');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { _electron: electron } = require('playwright-core');
const hubApi = require('../src/hub');
const seal = require('../src/seal');

const ROOT = path.join(__dirname, '..');
// Runs the source tree with the development Electron, or a packaged build
// when FETCH_APP_BIN points at one (dist/linux-unpacked/fetch-desktop).
const PACKAGED = process.env.FETCH_APP_BIN || '';
const ELECTRON = PACKAGED || require('electron');
const HUB_BIN = process.env.FETCH_HUB_BIN || path.join(ROOT, '..', 'hub', 'fetch');
const PORT = 18000 + Math.floor(Math.random() * 1000);
const HUB = `http://127.0.0.1:${PORT}`;
const TOKEN = 'e2e-token-0123456789';
const SHOTS = process.env.FETCH_SHOTS || '';

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
async function until(what, fn, ms = 10000) {
  const end = Date.now() + ms;
  for (;;) {
    const value = await fn();
    if (value) return value;
    if (Date.now() > end) throw new Error(`timed out waiting for ${what}`);
    await sleep(100);
  }
}
const step = (name) => console.log(`ok - ${name}`);

// A small real PNG (red and black checks) for pasting.
const PNG = Buffer.from(
  'iVBORw0KGgoAAAANSUhEUgAAABAAAAAQCAIAAACQkWg2AAAAM0lEQVR42mN8ZmPEAAN6Nx7B2Zc05LCKMzGQCGivgVFERISgu5HFB6EfWIhx92g80FwDAOb5FQSvgs1nAAAAAElFTkSuQmCC',
  'base64',
);

function startHub(dataDir) {
  return spawn(HUB_BIN, ['serve', '-addr', `127.0.0.1:${PORT}`, '-data', dataDir, '-token', TOKEN], { stdio: 'ignore' });
}

async function main() {
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'fetch-e2e-'));
  const profile = path.join(tmp, 'profile');
  const hubData = path.join(tmp, 'hub');
  fs.mkdirSync(profile);

  let hub = startHub(hubData);
  await until('the hub to start', () => fetch(HUB).then((r) => r.ok, () => false));

  // Another device (the test) sets the hub up; the app is given its key.
  const creds = await hubApi.setUp(HUB, TOKEN);
  const key = seal.keyFromText(creds.key);
  const link = seal.pairingLink({ hub: HUB, token: TOKEN, key });
  fs.writeFileSync(path.join(profile, 'config.json'), JSON.stringify({ ...creds, device: 'Test rig' }));

  const hubItems = async () => (await (await fetch(`${HUB}/api/items`, { headers: { Authorization: `Bearer ${TOKEN}` } })).json()).items
    .map((it) => {
      try { const o = seal.openMeta(key, Buffer.from(it.meta, 'base64')); return { ...it, encSize: it.size, ...o.meta, salt: o.salt }; } catch { return { ...it, broken: true }; }
    });
  const rawData = async (it) => seal.openBuffer(key, it.salt, Buffer.from(await (await fetch(`${HUB}/api/items/${it.id}/raw`, { headers: { Authorization: `Bearer ${TOKEN}` } })).arrayBuffer()));

  const launchArgs = ['--no-sandbox', `--user-data-dir=${profile}`, ...(PACKAGED ? [] : [ROOT])];
  const app = await electron.launch({ executablePath: ELECTRON, args: [...launchArgs, '--hidden'] });
  // Runs the app a second time with a flag, the way a system shortcut bound to
  // "fetch --throw" would. The second copy hands over to the first and exits.
  const again = (flag) => spawnSync(ELECTRON, [...launchArgs, flag].filter(Boolean), { stdio: 'ignore', timeout: 15000 });
  const windowFor = (part) => until(`the ${part} window`, () => app.windows().find((w) => w.url().includes(part)));
  const visible = (title) => app.evaluate(({ BrowserWindow }, t) => {
    const w = BrowserWindow.getAllWindows().find((x) => x.getTitle() === t);
    return Boolean(w && w.isVisible());
  }, title);
  const pageErrors = [];

  try {
    // ---- starts quietly, with the shortcut claimed ----
    const quick = await windowFor('quick/index.html');
    assert.equal(await visible('Throw'), false, 'the Throw box should start hidden');
    assert.equal(app.windows().length, 1, 'only the hidden Throw box exists after a --hidden start');
    assert.equal(await app.evaluate(({ globalShortcut }) => globalShortcut.isRegistered('Control+Alt+F')), true);
    step('starts hidden and registers Ctrl+Alt+F');

    // ---- the hotkey command opens the box; text and a picture go out encrypted ----
    again('--throw');
    await until('the Throw box to show', () => visible('Throw'));
    await quick.waitForFunction(() => document.activeElement && document.activeElement.id === 'text');
    await quick.fill('#text', 'hello from the desktop\nsecond line');
    await quick.evaluate((b64) => {
      const bytes = Uint8Array.from(atob(b64), (c) => c.charCodeAt(0));
      const data = new DataTransfer();
      data.items.add(new File([bytes], 'image.png', { type: 'image/png' }));
      document.dispatchEvent(new ClipboardEvent('paste', { clipboardData: data, bubbles: true, cancelable: true }));
    }, PNG.toString('base64'));
    await quick.waitForFunction(() => {
      const img = document.querySelector('.file img');
      return img && img.complete && img.naturalWidth > 0;
    });
    if (SHOTS) await quick.screenshot({ path: path.join(SHOTS, 'throw-box.png') });
    await quick.press('#text', 'Enter');
    await until('the box to close', async () => !(await visible('Throw')));
    const sent = await until('both items on the hub', async () => { const l = await hubItems(); return l.length === 2 && l; });
    const text = sent.find((i) => i.kind === 'text');
    const image = sent.find((i) => i.kind === 'image');
    assert.equal(text.text, 'hello from the desktop\nsecond line');
    assert.equal(text.device, 'Test rig');
    assert.match(image.name, /^image-\d{8}-\d{6}\.png$/);
    assert.deepEqual(await rawData(image), PNG, 'the picture decrypts byte for byte');
    const onDisk = Buffer.concat(fs.readdirSync(path.join(hubData, 'files')).map((f) => fs.readFileSync(path.join(hubData, 'files', f))));
    const index = fs.readFileSync(path.join(hubData, 'index.json'));
    for (const plain of ['hello from the desktop', 'Test rig', 'image/png']) assert.ok(!index.includes(plain) && !onDisk.includes(plain), plain);
    assert.ok(!onDisk.includes(PNG.subarray(0, 32)));
    step('Enter closes the box and uploads text and picture encrypted, in the background');

    // ---- a file copied in the file manager is streamed from disk ----
    const file = path.join(tmp, 'notes for later.txt');
    fs.writeFileSync(file, 'x'.repeat(300000));
    await quick.evaluate((p) => window.fetchQuick.throw({ id: 'disk', text: '', files: [{ path: p }] }), file);
    const fromDisk = await until('the file from disk', async () => (await hubItems()).find((i) => i.name === 'notes for later.txt'));
    assert.equal(fromDisk.size, 300000);
    assert.equal(fromDisk.encSize, seal.sealedSize(300000));
    assert.deepEqual(await rawData(fromDisk), Buffer.from('x'.repeat(300000)));
    step('a file on disk is encrypted as it streams, under its own name');

    // ---- the full window is the app's own copy of the page, already paired ----
    again('');
    const full = await windowFor('fetch-app://page');
    full.on('pageerror', (e) => pageErrors.push(String(e)));
    full.on('console', (m) => { if (m.type() === 'error') pageErrors.push(m.text()); });
    await full.waitForSelector('#app:not([hidden])');
    await full.waitForSelector('.item');
    assert.equal(await full.locator('.item').count(), 3);
    assert.equal(await full.isVisible('#setup'), false, 'no pairing screen: the app supplies the key');
    assert.equal(await full.textContent('#rename'), 'Test rig');
    assert.equal(await full.textContent('#footFp'), seal.fingerprint(key));
    assert.equal(await full.evaluate(() => window.isSecureContext), true);
    assert.equal(await full.locator('.item:has(.shot) .act', { hasText: /^Copy$/ }).count(), 1, 'pictures get a Copy button');
    await full.waitForFunction(() => [...document.images].every((i) => i.complete && i.naturalWidth > 0));
    // The page may only run its own scripts and talk only to the hub.
    const blocked = await full.evaluate(async () => { try { await fetch('https://example.com/'); return false; } catch { return true; } });
    assert.ok(blocked, 'the page could reach another address');
    if (SHOTS) await full.screenshot({ path: path.join(SHOTS, 'full-window.png') });
    step('the full window shows the decrypted feed from the built-in page, under a strict policy');

    // ---- code from elsewhere is highlighted; throws show up live ----
    await hubApi.send({ ...creds, device: 'CachyOS' }, { text: 'sudo pacman -Syu\nsystemctl reboot', files: [] });
    await full.waitForSelector('.item .lang');
    assert.equal(await full.textContent('.item .lang'), 'Shell');
    again('--throw');
    await until('the Throw box to show again', () => visible('Throw'));
    await quick.fill('#text', 'live update');
    await quick.click('#throw');
    await full.waitForFunction(() => document.querySelectorAll('.item').length === 5);
    step('code is highlighted on this computer, and the open window updates live');

    // ---- "Add a device" shows the pairing QR and link ----
    await full.click('#addDevice');
    await full.waitForSelector('#sheet:not([hidden])');
    assert.equal(await full.textContent('#sheetFp'), seal.fingerprint(key));
    await full.click('#copyLink');
    const copied = await until('the link on the clipboard', () => app.evaluate(({ clipboard }) => clipboard.readText()));
    assert.equal(copied, link, 'the copied pairing link');
    if (SHOTS) await full.screenshot({ path: path.join(SHOTS, 'add-device.png') });
    await full.click('#closeSheet');
    step('Add a device copies a pairing link for this hub and key');

    // ---- Escape keeps the draft; hub down: nothing is lost ----
    again('--throw');
    await until('the Throw box to show', () => visible('Throw'));
    await quick.keyboard.press('Escape');
    await until('Escape to close the box', async () => !(await visible('Throw')));
    hub.kill();
    await until('the hub to stop', () => fetch(HUB).then(() => false, () => true));
    again('--throw');
    await until('the Throw box to show', () => visible('Throw'));
    await quick.fill('#text', 'do not lose me');
    await quick.press('#text', 'Enter');
    await until('the box to close', async () => !(await visible('Throw')));
    await until('the draft to come back', async () => (await quick.inputValue('#text')) === 'do not lose me');
    hub = startHub(hubData);
    await until('the hub to return', () => fetch(HUB).then((r) => r.ok, () => false));
    again('--throw');
    await until('the Throw box to show', () => visible('Throw'));
    await quick.press('#text', 'Enter');
    await until('the retried item', async () => (await hubItems()).some((i) => i.text === 'do not lose me'));
    step('when the hub is unreachable the draft is put back, and retrying works');

    // ---- settings: unpair, a bad link, then pair with the real one ----
    again('--settings');
    const settings = await windowFor('settings/index.html');
    await settings.waitForSelector('#paired:not([hidden])');
    assert.equal(await settings.textContent('#hubName'), HUB);
    assert.equal(await settings.textContent('#fp'), seal.fingerprint(key));
    if (SHOTS) await settings.screenshot({ path: path.join(SHOTS, 'settings-paired.png') });
    settings.on('dialog', (d) => d.accept());
    await settings.click('#unpair');
    await settings.waitForSelector('#unpaired:not([hidden])');
    assert.equal(JSON.parse(fs.readFileSync(path.join(profile, 'config.json'), 'utf8')).key, '', 'unpairing forgets the key');
    await until('the full window to close', () => !app.windows().some((w) => w.url().includes('fetch-app://page')));

    await settings.fill('#link', seal.pairingLink({ hub: HUB, token: TOKEN, key: seal.newKey() }));
    await settings.click('#pair');
    await settings.waitForFunction(() => document.getElementById('hubError').textContent !== '');
    assert.match(await settings.textContent('#hubError'), /isn't the one the hub uses/);
    if (SHOTS) await settings.screenshot({ path: path.join(SHOTS, 'settings-unpaired.png') });

    await settings.fill('#link', link);
    await settings.click('#pair');
    await settings.waitForSelector('#paired:not([hidden])');
    const saved = JSON.parse(fs.readFileSync(path.join(profile, 'config.json'), 'utf8'));
    assert.equal(saved.key, creds.key);
    if (process.platform !== 'win32') assert.equal(fs.statSync(path.join(profile, 'config.json')).mode & 0o077, 0, 'the settings file is private');
    const reopened = await windowFor('fetch-app://page');
    await reopened.waitForSelector('.item');
    step('unpairing forgets the key, a stale link is refused, the right link pairs again');

    // ---- this computer's own settings ----
    await settings.fill('#device', 'Renamed rig');
    await settings.focus('#hotkey');
    await settings.keyboard.press('Control+Shift+KeyY');
    assert.equal(await settings.inputValue('#hotkey'), 'Ctrl + Shift + Y');
    await settings.click('#save');
    await until('settings to close', () => !app.windows().some((w) => w.url().includes('settings/index.html')));
    assert.equal(await app.evaluate(({ globalShortcut }) => globalShortcut.isRegistered('Control+Shift+Y')), true);
    assert.equal(await app.evaluate(({ globalShortcut }) => globalShortcut.isRegistered('Control+Alt+F')), false);
    again('--throw');
    await until('the Throw box to show', () => visible('Throw'));
    await quick.fill('#text', 'under a new name');
    await quick.press('#text', 'Enter');
    const renamed = await until('the renamed item', async () => (await hubItems()).find((i) => i.text === 'under a new name'));
    assert.equal(renamed.device, 'Renamed rig');
    step('a new name and shortcut are saved and used');

    // ---- the hub is started over elsewhere: this computer refuses to send with the old key ----
    await hubApi.setUp(HUB, TOKEN, { reset: true });
    again('--throw');
    await until('the Throw box to show', () => visible('Throw'));
    await quick.fill('#text', 'must not be sent with a stale key');
    await quick.press('#text', 'Enter');
    await until('the draft to come back', async () => (await quick.inputValue('#text')) === 'must not be sent with a stale key');
    assert.equal((await hubItems()).length, 0, 'nothing was sent');
    step("after a reset elsewhere, throws are held back instead of sent with a key nobody else has");

    // Expected: the blocked probe of example.com above, and the live-update
    // stream breaking off when the test stops the hub.
    const unexpected = pageErrors.filter((e) => !/example\.com|ERR_INCOMPLETE_CHUNKED_ENCODING/.test(e));
    assert.deepEqual(unexpected, []);
  } finally {
    await app.close().catch(() => {});
    hub.kill();
    fs.rmSync(tmp, { recursive: true, force: true });
  }
  console.log('all passed');
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
