'use strict';
// End-to-end check of the desktop app against a real hub.
//
//   go build -o /tmp/fetch ./hub          (from the repo root)
//   FETCH_HUB_BIN=/tmp/fetch xvfb-run -a node test/e2e.js
//
// Starts a hub, starts the app with a throwaway profile, and drives it the way
// a person would: hotkey command, paste, Throw, full window, settings.

const assert = require('node:assert/strict');
const { spawn, spawnSync } = require('node:child_process');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { _electron: electron } = require('playwright-core');

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
async function until(what, fn, ms = 8000) {
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
  const proc = spawn(HUB_BIN, ['serve', '-addr', `127.0.0.1:${PORT}`, '-data', dataDir, '-token', TOKEN], { stdio: 'ignore' });
  return proc;
}
async function hubItems() {
  const res = await fetch(`${HUB}/api/items`, { headers: { Authorization: `Bearer ${TOKEN}` } });
  return (await res.json()).items;
}

async function main() {
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'fetch-e2e-'));
  const profile = path.join(tmp, 'profile');
  const hubData = path.join(tmp, 'hub');
  fs.mkdirSync(profile);
  fs.writeFileSync(path.join(profile, 'config.json'), JSON.stringify({ url: HUB, token: TOKEN, device: 'Test rig' }));

  let hub = startHub(hubData);
  await until('the hub to start', () => fetch(HUB).then((r) => r.ok, () => false));

  const launchArgs = ['--no-sandbox', `--user-data-dir=${profile}`, ...(PACKAGED ? [] : [ROOT])];
  const app = await electron.launch({ executablePath: ELECTRON, args: [...launchArgs, '--hidden'] });
  // Runs the app a second time with a flag, the way a system shortcut bound to
  // "fetch --throw" would. The second copy hands over to the first and exits.
  const again = (flag) => spawnSync(ELECTRON, [...launchArgs, flag].filter(Boolean), { stdio: 'ignore', timeout: 15000 });
  const windowFor = (suffix) => until(`the ${suffix} window`, () => app.windows().find((w) => w.url().includes(suffix)));
  const visible = (title) => app.evaluate(({ BrowserWindow }, t) => {
    const w = BrowserWindow.getAllWindows().find((x) => x.getTitle() === t);
    return Boolean(w && w.isVisible());
  }, title);

  try {
    // ---- starts quietly, with the shortcut claimed ----
    const quick = await windowFor('quick/index.html');
    assert.equal(await visible('Throw'), false, 'the Throw box should start hidden');
    assert.equal(app.windows().length, 1, 'only the hidden Throw box exists after a --hidden start');
    assert.equal(await app.evaluate(({ globalShortcut }) => globalShortcut.isRegistered('Control+Alt+F')), true);
    assert.equal(await app.evaluate(({ app }) => app.getPath('userData')), profile);
    step('starts hidden and registers Ctrl+Alt+F');

    // ---- the hotkey command opens the box ----
    again('--throw');
    await until('the Throw box to show', () => visible('Throw'));
    await quick.waitForFunction(() => document.activeElement && document.activeElement.id === 'text');
    assert.equal(await quick.isDisabled('#throw'), true, 'nothing to throw yet');
    step('--throw shows the box with the text field focused');

    // ---- type, paste a picture, throw ----
    await quick.fill('#text', 'hello from the desktop\nsecond line');
    await quick.evaluate((b64) => {
      const bytes = Uint8Array.from(atob(b64), (c) => c.charCodeAt(0));
      const data = new DataTransfer();
      data.items.add(new File([bytes], 'image.png', { type: 'image/png' }));
      document.dispatchEvent(new ClipboardEvent('paste', { clipboardData: data, bubbles: true, cancelable: true }));
    }, PNG.toString('base64'));
    await quick.waitForFunction(() => {
      const img = document.querySelector('.file img');
      return img && img.complete && img.naturalWidth > 0; // the thumbnail really renders
    });
    assert.equal(await quick.isDisabled('#throw'), false);
    if (SHOTS) await quick.screenshot({ path: path.join(SHOTS, 'throw-box.png') });

    await quick.press('#text', 'Enter');
    await until('the box to close', async () => !(await visible('Throw')));
    const items = await until('both items on the hub', async () => {
      const list = await hubItems();
      return list.length === 2 && list;
    });
    const text = items.find((i) => i.kind === 'text');
    const image = items.find((i) => i.kind === 'image');
    assert.equal(text.text, 'hello from the desktop\nsecond line');
    assert.equal(text.device, 'Test rig');
    assert.equal(image.mime, 'image/png');
    assert.equal(image.size, PNG.length);
    assert.match(image.name, /^image-\d{8}-\d{6}\.png$/, 'clipboard pictures get a dated name from the hub');
    const stored = await fetch(`${HUB}/api/items/${image.id}/raw`, { headers: { Authorization: `Bearer ${TOKEN}` } });
    assert.deepEqual(Buffer.from(await stored.arrayBuffer()), PNG, 'the picture arrives byte for byte');
    assert.equal(await quick.inputValue('#text'), '', 'the box is empty for next time');
    assert.equal(await quick.locator('.file').count(), 0);
    step('Enter closes the box and uploads text and picture in the background');

    // ---- a file copied in the file manager is streamed from disk ----
    const onDisk = path.join(tmp, 'notes for later.txt');
    fs.writeFileSync(onDisk, 'x'.repeat(300000));
    // A synthetic paste can't carry a real path, so hand the app the same
    // message the box sends for a file that lives on disk.
    await quick.evaluate((file) => window.fetchQuick.throw({ id: 'disk', text: '', files: [{ path: file }] }), onDisk);
    const fromDisk = await until('the file from disk', async () => (await hubItems()).find((i) => i.name === 'notes for later.txt'));
    assert.equal(fromDisk.size, 300000);
    assert.equal(fromDisk.kind, 'file');
    step('a file on disk is uploaded by path with its own name');

    // ---- the full window is the hub page, already signed in ----
    again('');
    const full = await windowFor(`127.0.0.1:${PORT}`);
    await full.waitForSelector('.item');
    assert.equal(await full.locator('.item').count(), 3);
    assert.equal(await full.isVisible('#login'), false, 'no login screen: the app supplies the token');
    assert.equal(await full.textContent('#rename'), 'Test rig', 'the page uses the name from the app settings');
    assert.equal(await full.evaluate(() => window.isSecureContext), true, 'the home-network hub is trusted for clipboard use');
    assert.equal(await full.locator('.item:has(.shot) .act', { hasText: /^Copy$/ }).count(), 1, 'pictures get a Copy button');
    await full.waitForFunction(() => [...document.images].every((i) => i.complete && i.naturalWidth > 0));
    if (SHOTS) await full.screenshot({ path: path.join(SHOTS, 'full-window.png') });
    step('the full window shows the feed live, signed in, under the configured name');

    // ---- a throw shows up in the open full window without a refresh ----
    again('--throw');
    await until('the Throw box to show again', () => visible('Throw'));
    await quick.fill('#text', 'live update');
    await quick.click('#throw');
    await full.waitForFunction(() => document.querySelectorAll('.item').length === 4);
    step('the Throw button works and the open full window updates live');

    // ---- "Open Fetch" from the box ----
    again('--throw');
    await until('the Throw box to show', () => visible('Throw'));
    await quick.keyboard.press('Escape');
    await until('Escape to close the box', async () => !(await visible('Throw')));
    step('Escape closes the box');

    // ---- hub down: nothing is lost ----
    hub.kill();
    await until('the hub to stop', () => fetch(HUB).then(() => false, () => true));
    again('--throw');
    await until('the Throw box to show', () => visible('Throw'));
    await quick.fill('#text', 'do not lose me');
    await quick.press('#text', 'Enter');
    await until('the box to close', async () => !(await visible('Throw')));
    await until('the draft to come back', async () => (await quick.inputValue('#text')) === 'do not lose me');
    step('when the hub is unreachable the draft is put back');

    hub = startHub(hubData);
    await until('the hub to return', () => fetch(HUB).then((r) => r.ok, () => false));
    again('--throw');
    await until('the Throw box to show', () => visible('Throw'));
    assert.equal(await quick.inputValue('#text'), 'do not lose me');
    await quick.press('#text', 'Enter');
    await until('the retried item', async () => (await hubItems()).some((i) => i.text === 'do not lose me'));
    step('retrying the restored draft succeeds once the hub is back');

    // ---- settings: validation, then a real change ----
    again('--settings');
    const settings = await windowFor('settings/index.html');
    await settings.waitForFunction(() => document.getElementById('url').value !== '');
    assert.equal(await settings.inputValue('#url'), HUB);
    assert.equal(await settings.inputValue('#hotkey'), 'Ctrl + Alt + F');
    if (SHOTS) await settings.screenshot({ path: path.join(SHOTS, 'settings.png') });

    await settings.fill('#token', 'wrong-token-000000');
    await settings.click('#save');
    await settings.waitForFunction(() => document.getElementById('error').textContent !== '');
    assert.match(await settings.textContent('#error'), /rejected the token/);
    assert.equal(await settings.isVisible('#force'), true);
    assert.equal(JSON.parse(fs.readFileSync(path.join(profile, 'config.json'), 'utf8')).token, TOKEN, 'a failed check saves nothing');
    step('settings refuse a wrong token and keep the old one');

    await settings.fill('#token', TOKEN);
    await settings.fill('#device', 'Renamed rig');
    await settings.focus('#hotkey');
    await settings.keyboard.press('Control+Shift+KeyY');
    assert.equal(await settings.inputValue('#hotkey'), 'Ctrl + Shift + Y');
    await settings.click('#save');
    await until('settings to close', () => !app.windows().some((w) => w.url().includes('settings/index.html')));
    const saved = JSON.parse(fs.readFileSync(path.join(profile, 'config.json'), 'utf8'));
    assert.equal(saved.device, 'Renamed rig');
    assert.equal(saved.hotkey, 'Control+Shift+Y');
    assert.equal(await app.evaluate(({ globalShortcut }) => globalShortcut.isRegistered('Control+Shift+Y')), true);
    assert.equal(await app.evaluate(({ globalShortcut }) => globalShortcut.isRegistered('Control+Alt+F')), false);
    if (process.platform !== 'win32') {
      assert.equal(fs.statSync(path.join(profile, 'config.json')).mode & 0o077, 0, 'the settings file is private to the user');
    }
    step('settings save a new name and shortcut, and the shortcut is re-registered');

    again('--throw');
    await until('the Throw box to show', () => visible('Throw'));
    await quick.fill('#text', 'under a new name');
    await quick.press('#text', 'Enter');
    const renamed = await until('the renamed item', async () => (await hubItems()).find((i) => i.text === 'under a new name'));
    assert.equal(renamed.device, 'Renamed rig');
    step('later throws carry the new device name');
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
