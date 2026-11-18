'use strict';
// Helpers for handing over to a newer copy of the app (see handOver in
// main.js). Kept apart from Electron so they can be unit tested.

// True when version a (like "1.0.21") is newer than version b.
function isNewer(a, b) {
  const parts = (v) => String(v || '').split('-')[0].split('.').map((n) => parseInt(n, 10) || 0);
  const x = parts(a), y = parts(b);
  for (let i = 0; i < Math.max(x.length, y.length); i++) {
    if ((x[i] || 0) !== (y[i] || 0)) return (x[i] || 0) > (y[i] || 0);
  }
  return false;
}

// The environment to start the other copy with. An AppImage adds its own
// mount point to PATH, LD_LIBRARY_PATH and so on; that mount goes away when
// this copy quits, so those entries (and the AppImage's own variables) are
// dropped.
function cleanEnv(env) {
  const out = { ...env };
  const mount = env.APPDIR;
  for (const name of ['APPDIR', 'APPIMAGE', 'ARGV0', 'OWD']) delete out[name];
  if (!mount) return out;
  for (const [name, value] of Object.entries(out)) {
    if (typeof value !== 'string' || !value.includes(mount)) continue;
    const kept = value.split(':').filter((p) => p && !p.startsWith(mount));
    if (kept.length) out[name] = kept.join(':');
    else delete out[name];
  }
  return out;
}

// The arguments worth passing on (Electron adds its own switches to argv);
// --no-sandbox too, since a copy started with it can't run without it.
const OWN_FLAGS = ['--throw', '--settings', '--hidden', '--no-sandbox'];
const ownArgs = (argv) => argv.filter((a) => OWN_FLAGS.includes(a));

module.exports = { isNewer, cleanEnv, ownArgs };
