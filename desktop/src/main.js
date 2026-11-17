'use strict';
// Fetch for desktop: a hotkey pops up a small "Throw" box; whatever you put
// in it is uploaded to your hub in the background. The full window is the
// hub's own web page.

const {
  app, BrowserWindow, Menu, Notification, Tray,
  globalShortcut, ipcMain, nativeImage, screen, session, shell,
} = require('electron');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const config = require('./config');
const hub = require('./hub');

const APP_ID = 'com.horsehound.fetch';
const HUB_PARTITION = 'persist:hub';

app.setName('Fetch');
app.setAppUserModelId(APP_ID); // lets Windows attribute notifications to the app

const store = config.open(path.join(app.getPath('userData'), 'config.json'));

// Browsers only allow copying pictures to the clipboard on https pages. A hub
// on your home network is plain http, so tell the engine to trust that one
// address. This has to be decided before the app is ready, hence at startup.
const trustedHttpOrigin = insecureOrigin(store.get().url);
if (trustedHttpOrigin) {
  app.commandLine.appendSwitch('unsafely-treat-insecure-origin-as-secure', trustedHttpOrigin);
}

let tray = null;
let quick = null;
let full = null;
let settings = null;
let quitting = false;
let registeredHotkey = '';

if (!app.requestSingleInstanceLock()) {
  // Another copy is running; it receives our arguments (see 'second-instance').
  app.quit();
} else {
  app.on('second-instance', (_event, argv) => handleArgs(argv));
  app.whenReady().then(start);
}

function start() {
  if (process.platform !== 'darwin') Menu.setApplicationMenu(null); // macOS needs its menu for Cmd+C / Cmd+V
  prepareHubSession();
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
    await hub.send(store.get(), payload);
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
  const n = new Notification({ title, body });
  if (onClick) n.on('click', onClick);
  n.show();
}

// -------------------------------------------------------------- Full window

function insecureOrigin(url) {
  return url.startsWith('http://') ? new URL(url).origin : '';
}

// The full window shows the hub's own page. Requests to the hub get the token
// attached here, so there is no second login inside the app.
function prepareHubSession() {
  const ses = session.fromPartition(HUB_PARTITION);
  const cfg = store.get();
  ses.webRequest.onBeforeSendHeaders(
    { urls: config.isConfigured(cfg) ? [`${cfg.url}/*`] : ['http://invalid.invalid/*'] },
    (details, callback) => {
      details.requestHeaders.Authorization = `Bearer ${store.get().token}`;
      callback({ requestHeaders: details.requestHeaders });
    },
  );
  // The page needs to write to the clipboard and nothing else.
  const allowed = new Set(['clipboard-sanitized-write']);
  ses.setPermissionRequestHandler((_wc, permission, callback) => callback(allowed.has(permission)));
  ses.setPermissionCheckHandler((_wc, permission) => allowed.has(permission));
}

function showFull() {
  const cfg = store.get();
  if (!config.isConfigured(cfg)) return showSettings();
  if (full && !full.isDestroyed()) {
    if (full.isMinimized()) full.restore();
    full.show();
    full.focus();
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
    webPreferences: { partition: HUB_PARTITION, contextIsolation: true, sandbox: true },
  });
  const home = `${cfg.url}/?device=${encodeURIComponent(cfg.device)}`;
  full.loadURL(home);

  const wc = full.webContents;
  wc.on('did-fail-load', (_e, code, _desc, url, isMainFrame) => {
    if (!isMainFrame || code === -3) return; // -3: a navigation we cancelled ourselves
    full.loadFile(path.join(__dirname, 'offline.html'), { query: { hub: cfg.url, retry: home } });
  });
  // Only the hub lives in this window. Pictures open in a plain child window;
  // every other link goes to the real browser.
  const isHub = (url) => url.startsWith(`${cfg.url}/`);
  const isLocal = (url) => url.startsWith('file://');
  wc.setWindowOpenHandler(({ url }) => {
    if (isHub(url)) return { action: 'allow', overrideBrowserWindowOptions: { autoHideMenuBar: true, icon: appIcon() } };
    if (/^https?:\/\//.test(url)) shell.openExternal(url);
    return { action: 'deny' };
  });
  wc.on('will-navigate', (event, url) => {
    if (isHub(url) || isLocal(url)) return;
    event.preventDefault();
    if (/^https?:\/\//.test(url)) shell.openExternal(url);
  });
  full.on('closed', () => { full = null; });
}

// ----------------------------------------------------------------- Settings

function showSettings() {
  if (settings && !settings.isDestroyed()) {
    settings.show();
    settings.focus();
    return;
  }
  settings = new BrowserWindow({
    width: 460,
    height: 560,
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

ipcMain.handle('settings:get', (event) => {
  if (!fromSettings(event)) return null;
  return { ...store.get(), platform: process.platform, hotkeyActive: Boolean(registeredHotkey) };
});

// While the shortcut field is recording, our own hotkey must not fire.
ipcMain.on('settings:recording', (event, on) => {
  if (fromSettings(event)) globalShortcut.setSuspended(Boolean(on));
});

// Returns { ok: true } or { ok: false, field, message }. With `force`, a hub
// that can't be reached right now is saved anyway.
ipcMain.handle('settings:save', async (event, draft, force) => {
  if (!fromSettings(event)) return { ok: false, message: 'Not allowed.' };
  const before = store.get();
  const next = config.clean({ ...before, ...draft });

  if (!next.url) return { ok: false, field: 'url', message: "That address doesn't look right." };
  if (!next.token) return { ok: false, field: 'token', message: 'Enter the token the hub printed when it started.' };

  globalShortcut.setSuspended(false);
  if (!registerHotkey(next.hotkey)) {
    registerHotkey(before.hotkey);
    return { ok: false, field: 'hotkey', message: 'That shortcut is taken by another app, or the system won\'t allow it.' };
  }
  if (!force) {
    try {
      await hub.check(next);
    } catch (err) {
      registerHotkey(before.hotkey);
      return { ok: false, field: 'url', canForce: true, message: err instanceof hub.HubError ? err.message : 'Could not check the hub.' };
    }
  }

  store.set(next);
  syncLoginItem();
  prepareHubSession();
  rebuildTrayMenu();
  if (full && !full.isDestroyed()) full.destroy(); // it shows the old hub

  if (insecureOrigin(next.url) !== trustedHttpOrigin) {
    // The trusted-address switch is read once at startup; restart to apply it.
    const flags = new Set(['--hidden', '--throw', '--settings']);
    app.relaunch({
      execPath: process.env.APPIMAGE || process.execPath,
      args: process.argv.slice(1).filter((arg) => !flags.has(arg)), // come back with the full window
    });
    app.exit(0);
    return { ok: true };
  }
  if (settings && !settings.isDestroyed()) settings.close();
  showFull();
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
