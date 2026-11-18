'use strict';
// Fetch for desktop: a hotkey pops up a small "Throw" box; whatever you put
// in it is encrypted and uploaded to your hub in the background. The full
// window is the same page the hub serves, but the copy built into this app,
// so a hub that misbehaves can't hand it different code.

const {
  app, BrowserWindow, Menu, Notification, Tray,
  globalShortcut, ipcMain, nativeImage, protocol, screen, session, shell,
} = require('electron');
const { spawn } = require('node:child_process');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const config = require('./config');
const hub = require('./hub');
const seal = require('./seal');
const version = require('./version');

const APP_ID = 'com.horsehound.fetch';
const PAGE_SCHEME = 'fetch-app';
const PAGE_URL = `${PAGE_SCHEME}://page/index.html`;

app.setName('Fetch');
app.setAppUserModelId(APP_ID); // lets Windows attribute notifications to the app

const store = config.open(path.join(app.getPath('userData'), 'config.json'));

// The page and its scripts, as the hub serves them (hub/web in the repo).
const WEB_DIR = app.isPackaged ? path.join(process.resourcesPath, 'web') : path.join(__dirname, '..', '..', 'hub', 'web');

// The page is served from its own scheme, as a secure origin (so the
// clipboard works with any hub) with its own content policy.
protocol.registerSchemesAsPrivileged([
  { scheme: PAGE_SCHEME, privileges: { standard: true, secure: true, supportFetchAPI: true } },
]);

let tray = null;
let quick = null;
let full = null;
let settings = null;
let quitting = false;
let registeredHotkey = '';

// Which copy this is: a copy still running in the tray from before an update
// sees this and steps aside (see handOver).
const me = { version: app.getVersion(), exe: process.env.APPIMAGE || process.execPath };

if (!app.requestSingleInstanceLock(me)) {
  // Another copy is running; it receives our arguments (see 'second-instance').
  app.quit();
} else {
  app.on('second-instance', (_event, argv, cwd, other) => {
    if (app.isPackaged && other && version.isNewer(other.version, me.version)) handOver(other, argv, cwd);
    else handleArgs(argv);
  });
  app.whenReady().then(start);
}

// A newer copy was started while this one was still running: the app was
// updated underneath it. Without this, the new copy would just pass its
// arguments here and quit, and the hotkey would keep running the old code.
// So this copy lets go of everything and starts the new one in its place.
function handOver(other, argv, cwd) {
  console.log(`Fetch ${other.version} was started; handing over from ${me.version}.`);
  quitting = true;
  globalShortcut.unregisterAll();
  if (tray) { tray.destroy(); tray = null; }
  app.releaseSingleInstanceLock();
  try {
    spawn(other.exe, version.ownArgs(argv), { cwd, detached: true, stdio: 'ignore', env: version.cleanEnv(process.env) }).unref();
  } catch (err) {
    console.error(`Couldn't start ${other.exe}:`, err);
  }
  app.quit();
}

function start() {
  if (process.platform !== 'darwin') Menu.setApplicationMenu(null); // macOS needs its menu for Cmd+C / Cmd+V
  servePage();
  createQuick();
  createTray();
  const hotkey = store.get().hotkey;
  if (!registerHotkey(hotkey)) {
    notify("Fetch couldn't claim its shortcut", `${hotkey} is taken by another app or blocked by the system. Pick another in Settings.`, showSettings);
  }
  rebuildTrayMenu();
  syncLoginItem();

  if (!config.isConfigured(store.get())) showSettings();
  else handleArgs(process.argv, { atLaunch: true });

  app.on('activate', showFull); // macOS: clicking the dock icon
}

// `fetch --throw` opens the Throw box, `--settings` the settings, `--hidden`
// starts quietly in the tray (used at login). No flag opens the full window.
function handleArgs(argv, { atLaunch = false } = {}) {
  if (argv.includes('--throw')) showQuick();
  else if (argv.includes('--settings')) showSettings();
  else if (atLaunch && argv.includes('--hidden')) return;
  else showFull();
}

app.on('window-all-closed', () => {
  // Keep running in the tray; the hotkey is the point.
});
app.on('before-quit', () => { quitting = true; });
app.on('will-quit', () => globalShortcut.unregisterAll());

// ---------------------------------------------------------------- Throw box

function createQuick() {
  quick = new BrowserWindow({
    width: 680,
    height: 460,
    show: false,
    frame: false,
    transparent: true,
    hasShadow: false, // the page draws its own, around the rounded card
    resizable: false,
    minimizable: false,
    maximizable: false,
    fullscreenable: false,
    skipTaskbar: true,
    alwaysOnTop: true,
    title: 'Throw',
    icon: appIcon(),
    webPreferences: {
      preload: path.join(__dirname, 'quick', 'preload.js'),
      contextIsolation: true,
      sandbox: true,
    },
  });
  quick.setAlwaysOnTop(true, 'floating');
  quick.loadFile(path.join(__dirname, 'quick', 'index.html'));
  quick.on('blur', hideQuick);
  quick.on('close', (event) => {
    if (quitting) return;
    event.preventDefault();
    hideQuick();
  });
}

function showQuick() {
  if (!config.isConfigured(store.get())) return showSettings();
  if (quick.isVisible()) return hideQuick(); // the hotkey toggles
  // Upper middle of whichever screen the pointer is on.
  const area = screen.getDisplayNearestPoint(screen.getCursorScreenPoint()).workArea;
  const [width] = quick.getSize();
  quick.setPosition(
    Math.round(area.x + (area.width - width) / 2),
    Math.round(area.y + area.height * 0.2),
  );
  quick.show();
  quick.focus();
  quick.webContents.send('quick:shown');
}

function hideQuick() {
  if (quick && !quick.isDestroyed() && quick.isVisible()) quick.hide();
}

const fromQuick = (event) => quick && event.sender === quick.webContents;

ipcMain.on('quick:hide', (event) => { if (fromQuick(event)) hideQuick(); });
ipcMain.on('quick:open-full', (event) => { if (fromQuick(event)) { hideQuick(); showFull(); } });
ipcMain.on('quick:open-settings', (event) => { if (fromQuick(event)) { hideQuick(); showSettings(); } });

// The box closes at once; the upload carries on here in the background.
ipcMain.on('quick:throw', async (event, payload) => {
  if (!fromQuick(event)) return;
  hideQuick();
  try {
    const cfg = store.get();
    await hub.verify(cfg); // never send with a key the other devices don't have
    await hub.send(cfg, payload);
    quick.webContents.send('quick:thrown', payload.id);
  } catch (err) {
    // Hand the draft back so nothing is lost, and say so.
    quick.webContents.send('quick:failed', payload.id);
    notify("Couldn't throw that", `${err instanceof hub.HubError ? err.message : 'Something went wrong.'} It's back in the Throw box.`, showQuickForRetry);
    if (!(err instanceof hub.HubError)) console.error(err);
  }
});

function showQuickForRetry() {
  if (!quick.isVisible()) showQuick();
}

function notify(title, body, onClick) {
  if (!Notification.isSupported()) return;
  const n = new Notification({ title, body, icon: appIcon() });
  if (onClick) n.on('click', onClick);
  n.show();
}

// -------------------------------------------------------------- Full window

const PAGE_TYPES = { '.html': 'text/html; charset=utf-8', '.js': 'text/javascript; charset=utf-8' };

function servePage() {
  protocol.handle(PAGE_SCHEME, async (request) => {
    const u = new URL(request.url);
    const file = path.normalize(path.join(WEB_DIR, decodeURIComponent(u.pathname)));
    const type = PAGE_TYPES[path.extname(file)];
    if (u.host !== 'page' || !file.startsWith(WEB_DIR + path.sep) || !type) return new Response('not found', { status: 404 });
    let body;
    try { body = await fs.promises.readFile(file); } catch { return new Response('not found', { status: 404 }); }
    // The page holds the key: it may run only its own scripts and talk only to the hub.
    const hubOrigin = store.get().url || "'none'";
    return new Response(body, {
      headers: {
        'Content-Type': type,
        'Content-Security-Policy': "default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; " +
          `img-src 'self' blob: data:; connect-src ${hubOrigin}; base-uri 'none'; form-action 'none'`,
      },
    });
  });
  // The page needs to write to the clipboard and nothing else.
  const allowed = new Set(['clipboard-sanitized-write']);
  session.defaultSession.setPermissionRequestHandler((_wc, permission, callback) => callback(allowed.has(permission)));
  session.defaultSession.setPermissionCheckHandler((_wc, permission) => allowed.has(permission));
}

const fromFull = (event) => full && !full.isDestroyed() && event.sender === full.webContents;

// What the page needs to work: where the hub is, the token and the key.
ipcMain.on('page:config', (event) => {
  if (!fromFull(event)) { event.returnValue = null; return; }
  const cfg = store.get();
  event.returnValue = config.isConfigured(cfg) ? { hub: cfg.url, token: cfg.token, key: cfg.key, device: cfg.device } : null;
});
ipcMain.on('page:open-settings', (event) => { if (fromFull(event)) showSettings(); });
// "Start over" in the page: the hub was reset and has a new key.
ipcMain.handle('page:set-key', (event, key) => {
  if (!fromFull(event)) return;
  seal.keyFromText(key);
  store.set({ key });
  full.webContents.reload();
});

function showFull({ addDevice = false } = {}) {
  if (!config.isConfigured(store.get())) return showSettings();
  if (full && !full.isDestroyed()) {
    if (full.isMinimized()) full.restore();
    full.show();
    full.focus();
    if (addDevice) full.webContents.send('page:add-device');
    return;
  }
  full = new BrowserWindow({
    width: 820,
    height: 900,
    minWidth: 380,
    minHeight: 400,
    title: 'Fetch',
    icon: appIcon(),
    backgroundColor: '#1c1c1c',
    autoHideMenuBar: true,
    webPreferences: {
      preload: path.join(__dirname, 'page-preload.js'),
      contextIsolation: true,
      sandbox: true,
    },
  });
  full.loadURL(PAGE_URL);
  const wc = full.webContents;
  if (addDevice) wc.once('did-finish-load', () => wc.send('page:add-device'));
  // Pictures open in a plain child window; every other link goes to the real browser.
  wc.setWindowOpenHandler(({ url }) => {
    if (url.startsWith('blob:')) return { action: 'allow', overrideBrowserWindowOptions: { autoHideMenuBar: true, icon: appIcon(), backgroundColor: '#1c1c1c' } };
    if (/^https?:\/\//.test(url)) shell.openExternal(url);
    return { action: 'deny' };
  });
  wc.on('will-navigate', (event, url) => {
    if (url.startsWith(`${PAGE_SCHEME}://page/`)) return;
    event.preventDefault();
    if (/^https?:\/\//.test(url)) shell.openExternal(url);
  });
  full.on('closed', () => { full = null; });
}

function reloadFull() {
  if (full && !full.isDestroyed()) full.webContents.reload();
}

// ----------------------------------------------------------------- Settings

function showSettings() {
  if (settings && !settings.isDestroyed()) {
    settings.show();
    settings.focus();
    return;
  }
  settings = new BrowserWindow({
    width: 480,
    height: 660,
    resizable: false,
    minimizable: false,
    maximizable: false,
    fullscreenable: false,
    title: 'Fetch Settings',
    icon: appIcon(),
    backgroundColor: '#1c1c1c',
    autoHideMenuBar: true,
    webPreferences: {
      preload: path.join(__dirname, 'settings', 'preload.js'),
      contextIsolation: true,
      sandbox: true,
    },
  });
  settings.loadFile(path.join(__dirname, 'settings', 'index.html'));
  settings.on('closed', () => {
    settings = null;
    globalShortcut.setSuspended(false);
  });
}

const fromSettings = (event) => settings && event.sender === settings.webContents;

function settingsView() {
  const cfg = store.get();
  const paired = config.isConfigured(cfg);
  return {
    url: cfg.url, device: cfg.device, hotkey: cfg.hotkey, openAtLogin: cfg.openAtLogin,
    platform: process.platform, hotkeyActive: Boolean(registeredHotkey),
    paired, fingerprint: paired ? seal.fingerprint(seal.keyFromText(cfg.key)) : '',
  };
}

ipcMain.handle('settings:get', (event) => (fromSettings(event) ? settingsView() : null));

// While the shortcut field is recording, our own hotkey must not fire.
ipcMain.on('settings:recording', (event, on) => {
  if (fromSettings(event)) globalShortcut.setSuspended(Boolean(on));
});

function hubFailure(err) {
  const message = err instanceof hub.HubError || err.message ? err.message : 'Something went wrong.';
  return { ok: false, message, canReset: Boolean(err.canReset) };
}

function pairedWith(creds) {
  store.set(creds);
  rebuildTrayMenu();
  if (full && !full.isDestroyed()) reloadFull();
  else showFull();
  return { ok: true, view: settingsView() };
}

// Joins with a pairing link from another device.
ipcMain.handle('settings:pair', async (event, link) => {
  if (!fromSettings(event)) return { ok: false };
  try { return pairedWith(await hub.pair(link)); } catch (err) { return hubFailure(err); }
});

// First device: the hub's address and token; this computer creates the key.
ipcMain.handle('settings:set-up', async (event, url, token, reset) => {
  if (!fromSettings(event)) return { ok: false };
  try {
    const result = pairedWith(await hub.setUp(url, token, { reset: Boolean(reset) }));
    showFull({ addDevice: true });
    return result;
  } catch (err) { return hubFailure(err); }
});

ipcMain.handle('settings:unpair', (event) => {
  if (!fromSettings(event)) return null;
  store.set({ url: '', token: '', key: '' });
  if (full && !full.isDestroyed()) full.destroy();
  rebuildTrayMenu();
  return settingsView();
});

ipcMain.on('settings:add-device', (event) => { if (fromSettings(event)) showFull({ addDevice: true }); });

// This computer's own settings. Returns { ok: true } or { ok: false, field, message }.
ipcMain.handle('settings:save', (event, draft) => {
  if (!fromSettings(event)) return { ok: false, message: 'Not allowed.' };
  const before = store.get();
  const next = config.clean({ ...before, device: draft.device, hotkey: draft.hotkey, openAtLogin: draft.openAtLogin });
  globalShortcut.setSuspended(false);
  if (!registerHotkey(next.hotkey)) {
    registerHotkey(before.hotkey);
    return { ok: false, field: 'hotkey', message: 'That shortcut is taken by another app, or the system won\'t allow it.' };
  }
  store.set(next);
  syncLoginItem();
  rebuildTrayMenu();
  if (next.device !== before.device) reloadFull();
  if (settings && !settings.isDestroyed()) settings.close();
  if (config.isConfigured(next)) showFull();
  return { ok: true };
});

// ----------------------------------------------------- Hotkey, tray, login

// Returns false if the system refused the shortcut. An empty string means
// "no hotkey" and always succeeds.
function registerHotkey(accelerator) {
  if (registeredHotkey) globalShortcut.unregister(registeredHotkey);
  registeredHotkey = '';
  if (!accelerator) return true;
  let ok = false;
  try {
    ok = globalShortcut.register(accelerator, showQuick);
  } catch {
    ok = false; // not a valid accelerator string
  }
  if (ok) registeredHotkey = accelerator;
  return ok;
}

function asset(name) {
  return path.join(__dirname, '..', 'assets', name);
}

function appIcon() {
  return process.platform === 'linux' ? nativeImage.createFromPath(asset('icon.png')) : undefined;
}

function createTray() {
  // macOS tints "Template" images itself for light and dark menu bars.
  tray = new Tray(asset(process.platform === 'darwin' ? 'trayTemplate.png' : 'tray.png'));
  tray.setToolTip('Fetch');
  rebuildTrayMenu();
  if (process.platform === 'win32') tray.on('click', showQuick);
}

function rebuildTrayMenu() {
  if (!tray) return;
  tray.setContextMenu(Menu.buildFromTemplate([
    { label: 'Throw something…', accelerator: registeredHotkey || undefined, click: showQuick },
    { label: 'Open Fetch', click: showFull },
    { type: 'separator' },
    { label: 'Settings…', click: showSettings },
    { type: 'separator' },
    { label: `Fetch ${app.getVersion()}`, enabled: false },
    { label: 'Quit Fetch', click: () => app.quit() },
  ]));
}

// Start with the computer, quietly in the tray.
function syncLoginItem() {
  if (!app.isPackaged) return; // a development run shouldn't register itself
  const on = store.get().openAtLogin;
  if (process.platform !== 'linux') {
    app.setLoginItemSettings({ openAtLogin: on, args: ['--hidden'] });
    return;
  }
  const file = path.join(os.homedir(), '.config', 'autostart', `${APP_ID}.desktop`);
  if (!on) {
    fs.rmSync(file, { force: true });
    return;
  }
  const exec = process.env.APPIMAGE || process.execPath;
  fs.mkdirSync(path.dirname(file), { recursive: true });
  fs.writeFileSync(file, [
    '[Desktop Entry]',
    'Type=Application',
    'Name=Fetch',
    `Exec="${exec}" --hidden`,
    'X-GNOME-Autostart-enabled=true',
    '',
  ].join('\n'));
}
