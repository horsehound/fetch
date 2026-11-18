'use strict';
// End-to-end check of the hub's web page in a plain browser window, for
// spaces: invite links, public and private hubs, and deleting your space.
//
//   go build -o /tmp/fetch ./hub          (from the repo root)
//   FETCH_HUB_BIN=/tmp/fetch xvfb-run -a node test/web.e2e.js

const assert = require('node:assert/strict');
const { spawn } = require('node:child_process');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { _electron: electron } = require('playwright-core');
const seal = require('../src/seal');

const ROOT = path.join(__dirname, '..');
const HUB_BIN = process.env.FETCH_HUB_BIN || path.join(ROOT, '..', 'hub', 'fetch');
const TOKEN = 'web-e2e-token-0123456789';
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

// Starts a hub in its own folder; returns its address and a stop function.
async function startHub(tmp, mode) {
  const port = 19000 + Math.floor(Math.random() * 1000);
  const proc = spawn(HUB_BIN, ['serve', '-addr', `127.0.0.1:${port}`, '-data', path.join(tmp, mode), '-token', TOKEN, '-mode', mode],
    { stdio: 'ignore' });
  const url = `http://127.0.0.1:${port}`;
  await until(`the ${mode} hub to start`, () => fetch(url).then((r) => r.ok, () => false));
  return { url, stop: () => proc.kill() };
}

async function main() {
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'fetch-web-e2e-'));
  const app = await electron.launch({
    executablePath: require('electron'),
    args: ['--no-sandbox', `--user-data-dir=${path.join(tmp, 'profile')}`, path.join(__dirname, 'browser')],
  });
  const hubs = [];
  // Each page gets a fresh browser profile (its own storage), like a new device.
  let profiles = 0;
  async function open(url) {
    const partition = `fresh-${++profiles}`;
    const opened = app.waitForEvent('window');
    await app.evaluate(({ BrowserWindow }, [u, p]) => {
      const win = new BrowserWindow({ width: 820, height: 900, webPreferences: { partition: p } });
      win.loadURL(u);
    }, [url, partition]);
    const page = await opened;
    page.on('dialog', (d) => d.accept());
    await page.waitForLoadState('domcontentloaded');
    return page;
  }
  try {
    // ---- invite only: the host's invite link makes a space ----
    const hub = await startHub(tmp, 'invite');
    hubs.push(hub);
    const res = await fetch(`${hub.url}/api/invites`, { method: 'POST', headers: { Authorization: `Bearer ${TOKEN}` }, body: '{}' });
    const { code } = await res.json();
    const link = `${hub.url}/#invite=${code}`;
    assert.deepEqual(seal.parseInvite(link), { hub: hub.url, code });

    const plain = await open(hub.url + '/');
    await plain.waitForSelector('#joinBox:not([hidden])');
    assert.equal(await plain.textContent('#joinTitle'), 'New here?');
    assert.equal(await plain.isVisible('#joinCode'), true, 'invite hubs ask for a code');
    await plain.click('#joinButton');
    await plain.waitForFunction(() => /invite code/.test(document.getElementById('joinErr').textContent));
    if (SHOTS) await plain.screenshot({ path: path.join(SHOTS, 'web-invite-hub.png') });
    await plain.close();
    step('an invite-only hub asks newcomers for a code');

    const page = await open(link);
    await page.waitForFunction(() => document.getElementById('joinTitle').textContent === "You're invited");
    assert.equal(await page.inputValue('#joinCode'), code);
    assert.match(await page.textContent('#joinIntro'), /500 MB/);
    if (SHOTS) await page.screenshot({ path: path.join(SHOTS, 'web-invited.png') });
    await page.click('#joinButton');
    await page.waitForFunction(() => !document.getElementById('sheet').hidden);
    assert.match(await page.textContent('#sheetTitle'), /Your space is ready/);
    assert.equal(new URL(page.url()).hash, '', 'the code is gone from the address bar');
    await page.click('#closeSheet');
    await page.waitForFunction(() => / of 500 MB used/.test(document.getElementById('policy').textContent));
    assert.equal(await page.isVisible('#hostLine'), false, "a guest can't invite");
    assert.equal(await page.isVisible('#leaveWrap'), true);
    await page.fill('#draft', 'hello from my own space');
    await page.press('#draft', 'Enter');
    await page.waitForSelector('.item');
    const token = await page.evaluate(() => localStorage.getItem('fetch.token'));
    assert.notEqual(token, TOKEN);
    const own = await (await fetch(`${hub.url}/api/items`, { headers: { Authorization: `Bearer ${TOKEN}` } })).json();
    assert.equal(own.items.length, 0, "nothing landed in the host's feed");
    step('an invite link opens the page, makes a space of its own, and the browser works in it');

    // The same link again, in another fresh browser: used up.
    const again = await open(link);
    await again.waitForSelector('#joinBox:not([hidden])');
    await again.click('#joinButton');
    await again.waitForFunction(() => /isn't valid/.test(document.getElementById('joinErr').textContent));
    await again.close();
    step('an invite link works once');

    // ---- leaving: the space is deleted ----
    await page.click('#leave');
    await page.waitForFunction(() => !document.getElementById('setup').hidden);
    assert.match(await page.textContent('#setupReason'), /deleted/);
    const gone = await fetch(`${hub.url}/api/items`, { headers: { Authorization: `Bearer ${token}` } });
    assert.equal(gone.status, 401);
    await page.close();
    step('Delete my space removes it from the hub');

    // ---- the host's own browser: Invite someone ----
    const host = await open(hub.url + '/');
    await host.waitForSelector('#firstDevice');
    await host.click('#firstDevice summary');
    await host.fill('#initToken', TOKEN);
    await host.click('#initForm button');
    await host.waitForFunction(() => !document.getElementById('sheet').hidden);
    await host.click('#closeSheet');
    await host.waitForSelector('#hostLine:not([hidden])');
    assert.equal(await host.isVisible('#leaveWrap'), false, "the host's own feed can't be deleted");
    await host.click('#invite');
    await host.waitForFunction(() => document.getElementById('sheetTitle').textContent === 'Invite someone');
    await host.close();
    step("the host sets up their own feed with the token, and can invite from there");

    // ---- public: anyone can make a space, no code needed ----
    const pub = await startHub(tmp, 'public');
    hubs.push(pub);
    const p = await open(pub.url + '/');
    await p.waitForSelector('#joinBox:not([hidden])');
    assert.equal(await p.isVisible('#joinCode'), false, 'no code on a public hub');
    if (SHOTS) await p.screenshot({ path: path.join(SHOTS, 'web-public-hub.png') });
    await p.click('#joinButton');
    await p.waitForFunction(() => !document.getElementById('sheet').hidden);
    await p.close();
    step('a public hub makes a space with one click');

    // ---- private: nothing offered ----
    const priv = await startHub(tmp, 'private');
    hubs.push(priv);
    const q = await open(priv.url + '/');
    await q.waitForSelector('#setup:not([hidden])');
    await sleep(500); // give the page time to ask the hub
    assert.equal(await q.isVisible('#joinBox'), false, 'a private hub offers no spaces');
    await q.close();
    step('a private hub offers no spaces');
  } finally {
    await app.close().catch(() => {});
    for (const h of hubs) h.stop();
    fs.rmSync(tmp, { recursive: true, force: true });
  }
  console.log('all passed');
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
