'use strict';
// Settings for the desktop app, kept as one small JSON file in the user's
// config directory. No Electron imports, so this file is unit-testable.

const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { normalizeURL } = require('./seal');

const DEFAULT_HOTKEY = 'Control+Alt+F';

function defaults() {
  return {
    url: '',
    token: '',
    device: os.hostname() || 'Computer',
    hotkey: DEFAULT_HOTKEY,
    openAtLogin: false,
  };
}

function clean(raw) {
  const d = defaults();
  const text = (v, fallback, max) => (typeof v === 'string' && v.trim() ? v.trim().slice(0, max) : fallback);
  return {
    url: normalizeURL(raw.url),
    token: text(raw.token, '', 200),
    // The encryption key, as in a pairing link. Never leaves this computer
    // except in a pairing link you copy yourself.
    key: typeof raw.key === 'string' && /^[A-Za-z0-9_-]{43}$/.test(raw.key.trim()) ? raw.key.trim() : '',
    device: text(raw.device, d.device, 40),
    hotkey: typeof raw.hotkey === 'string' ? raw.hotkey.trim() : d.hotkey, // '' means "no hotkey"
    openAtLogin: raw.openAtLogin === true,
  };
}

function isConfigured(cfg) {
  return Boolean(cfg.url && cfg.token && cfg.key);
}

function open(file) {
  let current = defaults();
  try {
    current = clean({ ...defaults(), ...JSON.parse(fs.readFileSync(file, 'utf8')) });
  } catch {
    // First run, or an unreadable file: start from defaults.
  }
  return {
    get: () => ({ ...current }),
    set(next) {
      current = clean({ ...current, ...next });
      fs.mkdirSync(path.dirname(file), { recursive: true });
      // The token and the key unlock the feed; keep the file private to this user.
      fs.writeFileSync(file, JSON.stringify(current, null, 2) + '\n', { mode: 0o600 });
      fs.chmodSync(file, 0o600); // the mode above only applies to a newly created file
      return { ...current };
    },
  };
}

module.exports = { DEFAULT_HOTKEY, defaults, normalizeURL, clean, isConfigured, open };
