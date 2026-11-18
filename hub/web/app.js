/*
 * The Fetch page: in a browser (served by the hub) and inside the desktop app
 * (bundled with it). Everything is encrypted and decrypted here; the hub only
 * sees scrambled data.
 */
(() => {
'use strict';
const C = window.FetchCrypto;
const Code = window.FetchCode;
// The desktop app hands over its settings; a browser keeps its own.
const desktop = window.fetchDesktop || null;
const $ = (id) => document.getElementById(id);

const store = {
  get(k) { try { return localStorage.getItem(k); } catch { return null; } },
  set(k, v) { try { localStorage.setItem(k, v); } catch {} },
  remove(k) { try { localStorage.removeItem(k); } catch {} },
};

function el(tag, props, ...kids) {
  const n = document.createElement(tag);
  for (const [k, v] of Object.entries(props || {})) {
    if (k === 'class') n.className = v;
    else if (k === 'text') n.textContent = v;
    else if (k.startsWith('on')) n.addEventListener(k.slice(2), v);
    else n.setAttribute(k, v);
  }
  n.append(...kids);
  return n;
}

function guessDevice() {
  const ua = navigator.userAgent;
  if (/iPhone/.test(ua)) return 'iPhone';
  if (/Android/.test(ua)) return 'Android';
  if (/Windows/.test(ua)) return 'Windows PC';
  if (/Mac OS X/.test(ua)) return 'Mac';
  if (/Linux/.test(ua)) return 'Linux laptop';
  return 'Browser';
}

// ---- who we are: hub, token, key ----

// In a browser the page talks to the hub it came from (hub = ''); in the
// desktop app it talks to the configured address.
let cfg = null;

function loadConfig() {
  if (desktop) {
    const c = desktop.config();
    if (!c || !c.hub || !c.token || !c.key) return null;
    try { return { hub: c.hub, token: c.token, key: C.keyFromText(c.key), device: c.device || 'Computer' }; } catch { return null; }
  }
  const token = store.get('fetch.token'), key = store.get('fetch.key');
  if (!token || !key) return null;
  try { return { hub: '', token, key: C.keyFromText(key), device: store.get('fetch.device') || guessDevice() }; } catch { return null; }
}

function saveBrowserConfig(token, key) {
  store.set('fetch.token', token);
  store.set('fetch.key', C.keyToText(key));
}

function forgetBrowserConfig() {
  store.remove('fetch.token');
  store.remove('fetch.key');
}

// The hub address other devices should use: this page's own, unless the
// desktop app says otherwise.
function hubAddress() { return cfg.hub || location.origin; }

// ---- talking to the hub ----

class HubError extends Error {}
class Locked extends HubError {}

async function api(path, opts = {}, who = cfg) {
  const headers = Object.assign({ Authorization: 'Bearer ' + who.token }, opts.headers);
  let res;
  try {
    res = await fetch((who.hub || '') + path, Object.assign({}, opts, { headers }));
  } catch {
    throw new HubError("Can't reach the hub");
  }
  if (res.status === 401) throw new Locked('The hub rejected the token');
  if (!res.ok) throw new HubError((await res.text()).trim() || 'The hub answered ' + res.status);
  return res;
}

async function hubCheck(who) {
  const res = await api('/api/vault', {}, who);
  let v;
  try { v = await res.json(); } catch { v = null; }
  if (!v || typeof v.check !== 'string') throw new HubError("This hub doesn't support encryption yet. Update it first.");
  return v.check;
}

function claimHub(who) {
  return api('/api/vault', { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ check: C.check(who.key) }) }, who);
}

let toastTimer;
function toast(msg, ms = 1800) {
  const t = $('toast');
  t.textContent = msg;
  t.classList.add('show');
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => t.classList.remove('show'), ms);
}
function fail(e) {
  if (e instanceof Locked) return locked();
  toast(e && e.message ? e.message : String(e), 3200);
}

// ---- setting up (browser only; the desktop app does this in its settings) ----

function showSetup(reason) {
  stopListening();
  $('app').hidden = true;
  $('setup').hidden = false;
  $('setupReason').hidden = !reason;
  $('setupReason').textContent = reason || '';
  $('pairLink').focus();
}

function locked() {
  if (desktop) {
    showNotice("The hub rejected this computer's token. Pair it again in Settings.", 'Open Settings', () => desktop.openSettings());
    return;
  }
  forgetBrowserConfig();
  showSetup('The hub no longer accepts this browser. Pair it again.');
}

$('pairForm').addEventListener('submit', async (e) => {
  e.preventDefault();
  $('pairErr').textContent = '';
  let p;
  try { p = C.parsePairing($('pairLink').value); } catch (err) { $('pairErr').textContent = err.message; return; }
  const who = { hub: '', token: p.token, key: p.key };
  try {
    const check = await hubCheck(who);
    if (check === '') await claimHub(who); // the hub was reset since the link was made
    else if (check !== C.check(p.key)) {
      $('pairErr').textContent = "This link's key isn't the one the hub uses now. Make a fresh link on a device that works.";
      return;
    }
  } catch (err) {
    $('pairErr').textContent = err instanceof Locked ? "The hub doesn't accept this link's token. Is the link for this hub?" : err.message;
    return;
  }
  saveBrowserConfig(p.token, p.key);
  $('pairLink').value = '';
  start();
  toast('Paired');
});

$('initForm').addEventListener('submit', async (e) => {
  e.preventDefault();
  $('initErr').textContent = '';
  $('initReset').hidden = true;
  const token = $('initToken').value.trim();
  if (!token) return;
  const who = { hub: '', token, key: C.newKey() };
  try {
    if (await hubCheck(who) !== '') {
      $('initErr').textContent = 'This hub already has a key. Pair from a device that has it, or start over.';
      $('initReset').hidden = false;
      return;
    }
    await claimHub(who);
  } catch (err) {
    $('initErr').textContent = err instanceof Locked ? "That isn't the hub's token." : err.message;
    return;
  }
  saveBrowserConfig(token, who.key);
  $('initToken').value = '';
  start().then(() => openSheet('Now add your other devices'));
});

$('initReset').addEventListener('click', async () => {
  const token = $('initToken').value.trim();
  if (!confirm('Delete everything on the hub, pinned items too, and start over with a new key? Every other device will have to be paired again.')) return;
  const who = { hub: '', token, key: C.newKey() };
  try {
    await api('/api/vault', { method: 'DELETE' }, who);
    await claimHub(who);
  } catch (err) {
    $('initErr').textContent = err.message;
    return;
  }
  saveBrowserConfig(token, who.key);
  start().then(() => openSheet('Now pair your other devices again'));
});

// ---- adding a device: QR code and link ----

function drawQR(canvas, text) {
  const qr = window.FetchVendor.qrcode(0, 'M');
  qr.addData(text);
  qr.make();
  const n = qr.getModuleCount(), quiet = 4, scale = 8;
  const size = (n + quiet * 2) * scale;
  canvas.width = canvas.height = size;
  const g = canvas.getContext('2d');
  g.fillStyle = '#fff';
  g.fillRect(0, 0, size, size);
  g.fillStyle = '#000';
  for (let r = 0; r < n; r++) {
    for (let c = 0; c < n; c++) if (qr.isDark(r, c)) g.fillRect((c + quiet) * scale, (r + quiet) * scale, scale, scale);
  }
}

function pairing() { return C.pairingLink({ hub: hubAddress(), token: cfg.token, key: cfg.key }); }

function openSheet(title) {
  $('sheetTitle').textContent = title || 'Add a device';
  drawQR($('qr'), pairing());
  $('sheetFp').textContent = C.fingerprint(cfg.key);
  $('sheet').hidden = false;
  $('copyLink').focus();
}
function closeSheet() { $('sheet').hidden = true; }

$('addDevice').addEventListener('click', () => openSheet());
$('closeSheet').addEventListener('click', closeSheet);
$('sheet').addEventListener('click', (e) => { if (e.target === $('sheet')) closeSheet(); });
document.addEventListener('keydown', (e) => { if (e.key === 'Escape' && !$('sheet').hidden) closeSheet(); });
$('copyLink').addEventListener('click', () => copyText(pairing(), 'Link copied. Paste it on your other device.').catch(fail));

$('startOver').addEventListener('click', async () => {
  if (!confirm('Start over with a new key? This deletes everything on the hub, pinned items too, and every other device has to be paired again.')) return;
  const key = C.newKey();
  try {
    await api('/api/vault', { method: 'DELETE' });
    await claimHub({ hub: cfg.hub, token: cfg.token, key });
  } catch (err) { fail(err); return; }
  if (desktop) {
    await desktop.setKey(C.keyToText(key)); // the app saves it and reloads this page
    return;
  }
  saveBrowserConfig(cfg.token, key);
  cfg.key = key;
  forgetItems();
  openSheet('Now pair your other devices again');
  load();
});

// ---- sending ----

const INLINE_IMAGE = new Set(['image/png', 'image/jpeg', 'image/gif', 'image/webp', 'image/avif', 'image/bmp']);
const EXT = { 'image/png': '.png', 'image/jpeg': '.jpg', 'image/gif': '.gif', 'image/webp': '.webp', 'image/avif': '.avif', 'image/bmp': '.bmp', 'text/plain': '.txt', 'application/pdf': '.pdf' };

function stamp() {
  const d = new Date(), p = (n) => String(n).padStart(2, '0');
  return `${d.getFullYear()}${p(d.getMonth() + 1)}${p(d.getDate())}-${p(d.getHours())}${p(d.getMinutes())}${p(d.getSeconds())}`;
}

async function upload(meta, blob) {
  meta.device = cfg.device;
  const { envelope, salt } = C.sealMeta(cfg.key, meta);
  const chunks = blob ? await C.sealBlob(cfg.key, salt, blob) : [];
  await api('/api/items', { method: 'POST', headers: { 'Content-Type': C.SEALED_TYPE }, body: new Blob(C.uploadParts(envelope, chunks)) });
}

const utf8 = new TextEncoder();

async function sendText(text) {
  if (!text.trim()) return;
  if (utf8.encode(text).length > C.MAX_TEXT_IN_META) {
    return sendFile(new File([text], `text-${stamp()}.txt`, { type: 'text/plain' }));
  }
  await upload({ kind: 'text', text }, null);
}

async function sendFile(file, keepName = true) {
  const mime = file.type || 'application/octet-stream';
  const kind = INLINE_IMAGE.has(mime) ? 'image' : 'file';
  const name = keepName && file.name ? file.name : `${kind}-${stamp()}${EXT[mime] || ''}`;
  if (file.size > 20e6) toast('Encrypting ' + size(file.size) + '…', 4000);
  await upload({ kind, name, mime, size: file.size }, file);
}

const track = (promise) => promise.catch(fail);

const draft = $('draft');
function fitDraft() {
  draft.style.height = 'auto';
  draft.style.height = draft.scrollHeight + 'px';
  $('send').disabled = !draft.value.trim();
}
function sendDraft() {
  const text = draft.value;
  if (!text.trim()) return;
  draft.value = '';
  fitDraft();
  track(sendText(text));
}
draft.addEventListener('input', fitDraft);
draft.addEventListener('keydown', (e) => {
  if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) { e.preventDefault(); sendDraft(); }
});
$('send').addEventListener('click', sendDraft);
$('attach').addEventListener('click', () => $('picker').click());
$('picker').addEventListener('change', (e) => {
  [...e.target.files].forEach((f) => track(sendFile(f)));
  e.target.value = '';
});

// Paste anywhere: pictures and files always go straight out; text goes
// straight out unless you are typing in the box.
document.addEventListener('paste', (e) => {
  if ($('app').hidden || !$('sheet').hidden || !e.clipboardData) return;
  const files = [...e.clipboardData.files];
  if (files.length) {
    e.preventDefault();
    files.forEach((f) => track(sendFile(f, f.name !== 'image.png')));
    return;
  }
  if (e.target === draft) return;
  const text = e.clipboardData.getData('text/plain');
  if (text.trim()) { e.preventDefault(); track(sendText(text)); }
});

let dragDepth = 0;
const hasPayload = (e) => !$('app').hidden && e.dataTransfer && [...e.dataTransfer.types].some((t) => t === 'Files' || t === 'text/plain');
window.addEventListener('dragenter', (e) => { if (hasPayload(e)) { dragDepth++; $('drop').hidden = false; } });
window.addEventListener('dragleave', () => { if (--dragDepth <= 0) { dragDepth = 0; $('drop').hidden = true; } });
window.addEventListener('dragover', (e) => { if (hasPayload(e)) e.preventDefault(); });
window.addEventListener('drop', (e) => {
  dragDepth = 0; $('drop').hidden = true;
  if (!hasPayload(e)) return;
  e.preventDefault();
  const files = [...e.dataTransfer.files];
  if (files.length) files.forEach((f) => track(sendFile(f)));
  else track(sendText(e.dataTransfer.getData('text/plain')));
});

// ---- reading and decrypting ----

// Each item's decrypted metadata, by id. Items never change once uploaded.
const opened = new Map();
// Decrypted pictures and files, as Blobs, by id.
const blobs = new Map();
const objectURLs = new Map();

function openItem(item) {
  let o = opened.get(item.id);
  if (!o) {
    try {
      const r = C.openMeta(cfg.key, C.fromBase64(item.meta));
      const m = r.meta;
      o = {
        kind: ['text', 'image', 'file'].includes(m.kind) ? m.kind : 'file',
        text: typeof m.text === 'string' ? m.text : '',
        name: typeof m.name === 'string' && m.name ? m.name : 'file',
        mime: typeof m.mime === 'string' && m.mime ? m.mime : 'application/octet-stream',
        device: typeof m.device === 'string' ? m.device : '',
        // The plaintext size: from the metadata, or worked out from the encrypted size.
        size: typeof m.size === 'number' && m.size > 0 ? m.size : plainSize(item.size),
        salt: r.salt,
      };
      if (o.kind === 'text') o.code = Code.analyze(o.text);
      if (o.kind === 'image' && !INLINE_IMAGE.has(o.mime)) o.kind = 'file';
    } catch {
      o = { broken: true };
    }
    opened.set(item.id, o);
  }
  return Object.assign({}, item, o);
}

async function decryptedBlob(item) {
  if (blobs.has(item.id)) return blobs.get(item.id);
  const promise = (async () => {
    const res = await api('/api/items/' + item.id + '/raw');
    const opener = C.dataOpener(cfg.key, item.salt);
    const parts = [];
    const reader = res.body.getReader();
    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      parts.push(...opener.push(value));
    }
    parts.push(...opener.end());
    return new Blob(parts, { type: item.mime });
  })();
  blobs.set(item.id, promise);
  promise.catch(() => blobs.delete(item.id));
  return promise;
}

async function objectURL(item) {
  if (!objectURLs.has(item.id)) objectURLs.set(item.id, decryptedBlob(item).then((b) => URL.createObjectURL(b)));
  return objectURLs.get(item.id);
}

function forgetItems() {
  for (const p of objectURLs.values()) p.then((u) => URL.revokeObjectURL(u), () => {});
  opened.clear();
  blobs.clear();
  objectURLs.clear();
  for (const li of nodes.values()) li.remove();
  nodes.clear();
  items = [];
}

// ---- copying and saving ----

async function copyText(text, msg = 'Copied') {
  if (navigator.clipboard && window.isSecureContext) {
    await navigator.clipboard.writeText(text);
  } else {
    // Plain-http pages have no clipboard API; the old command still works.
    const ta = el('textarea', { style: 'position:fixed;opacity:0' });
    ta.value = text;
    document.body.append(ta);
    ta.select();
    const ok = document.execCommand('copy');
    ta.remove();
    if (!ok) throw new Error('Copy was blocked by the browser');
  }
  toast(msg);
}

const canCopyImages = !!(window.isSecureContext && navigator.clipboard && window.ClipboardItem);
async function pngBlob(item) {
  const blob = await decryptedBlob(item);
  if (blob.type === 'image/png') return blob;
  const bmp = await createImageBitmap(blob); // the clipboard only takes PNG
  const canvas = el('canvas', { width: bmp.width, height: bmp.height });
  canvas.getContext('2d').drawImage(bmp, 0, 0);
  return new Promise((ok, no) => canvas.toBlob((b) => (b ? ok(b) : no(new Error('Could not convert the picture'))), 'image/png'));
}
async function copyImage(item) {
  await navigator.clipboard.write([new ClipboardItem({ 'image/png': pngBlob(item) })]);
  toast('Picture copied');
}

async function save(item) {
  const blob = await decryptedBlob(item);
  const url = URL.createObjectURL(blob);
  const a = el('a', { href: url, download: item.name });
  document.body.append(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 60000);
}

async function view(item) {
  window.open(await objectURL(item), '_blank', 'noopener');
}

// ---- the feed ----

const isLink = (t) => /^https?:\/\/\S+$/.test(t.trim());

function size(n) {
  if (n < 1000) return n + ' B';
  const units = ['kB', 'MB', 'GB'];
  let i = -1;
  do { n /= 1000; i++; } while (n >= 1000 && i < units.length - 1);
  return n.toFixed(n < 10 ? 1 : 0) + ' ' + units[i];
}
function ago(ms) {
  const s = Math.max(0, (Date.now() - ms) / 1000);
  if (s < 45) return 'just now';
  if (s < 3600) return Math.round(s / 60) + ' min ago';
  if (s < 86400) return Math.round(s / 3600) + ' h ago';
  if (s < 86400 * 7) return Math.round(s / 86400) + ' d ago';
  return new Date(ms).toLocaleDateString();
}
// Plaintext bytes of n bytes of encrypted data: each chunk carries a 16-byte tag.
function plainSize(n) {
  if (!n) return 0;
  return n - Math.ceil(n / (C.CHUNK + 16)) * 16;
}

function build(item) {
  const li = el('li', { class: 'item' });
  const actions = el('span', { class: 'actions' });
  const act = (label, fn, cls) => {
    const b = el('button', { class: 'act' + (cls ? ' ' + cls : ''), type: 'button', text: label, onclick: () => Promise.resolve(fn()).catch(fail) });
    actions.append(b);
    return b;
  };

  if (item.broken) {
    li.classList.add('broken');
    li.append(el('div', { class: 'broken-note', text: "Can't decrypt this item: it was sent with a different key." }));
  } else if (item.kind === 'text') {
    const body = el('button', { class: 'text', type: 'button', title: 'Click to copy', onclick: () => copyText(item.text).catch(fail) });
    if (item.code) {
      body.append(...item.code.tokens.map(([cls, text]) => (cls ? el('span', { class: 't-' + cls, text }) : text)));
      li.append(el('div', { class: 'code' }, el('span', { class: 'lang', text: item.code.label }), body));
    } else {
      body.textContent = item.text;
      li.append(body);
    }
    if (item.text.length > 700 || item.text.split('\n').length > 9) {
      body.classList.add('clamped');
      const more = act('Show all', () => { body.classList.remove('clamped'); more.remove(); });
    }
    if (isLink(item.text)) {
      actions.append(el('a', { class: 'act', href: item.text.trim(), target: '_blank', rel: 'noopener noreferrer', text: 'Open' }));
    }
    act('Copy', () => copyText(item.text));
  } else if (item.kind === 'image') {
    const img = el('img', { alt: item.name });
    li.append(el('button', { class: 'shot', type: 'button', title: 'Open', onclick: () => view(item).catch(fail) }, img));
    objectURL(item).then((u) => { img.src = u; }, () => { img.alt = "Couldn't load the picture"; });
    if (canCopyImages) act('Copy', () => copyImage(item));
    act('Save', () => save(item));
  } else {
    const ext = (item.name.includes('.') ? item.name.split('.').pop() : 'file').slice(0, 5);
    li.append(el('div', { class: 'file' },
      el('span', { class: 'ext', text: ext }),
      el('span', { style: 'min-width:0' },
        el('div', { class: 'name', text: item.name, title: item.name }),
        el('div', { class: 'size', text: size(item.size) }))));
    act('Save', () => save(item));
  }

  if (!item.broken) li._pin = act('Pin', () => api('/api/items/' + item.id + '/pin?on=' + (li._item.pinned ? 0 : 1), { method: 'PUT' }));
  act('Delete', () => api('/api/items/' + item.id, { method: 'DELETE' }), 'danger');

  li._from = el('span', { class: 'from' });
  li.append(el('div', { class: 'meta' }, li._from, actions));
  return li;
}

function paint(li, item) {
  li._item = item;
  li.classList.toggle('pinned', item.pinned);
  if (li._pin) {
    li._pin.textContent = item.pinned ? 'Pinned' : 'Pin';
    li._pin.classList.toggle('on', item.pinned);
    li._pin.title = item.pinned ? 'Unpin: let this expire again' : 'Keep this until you delete it';
  }
  const bits = [item.device || (item.broken ? 'unknown device' : 'unknown'), ago(item.created)];
  if (item.kind === 'image') bits.push(size(item.size));
  li._from.textContent = bits.join(' · ');
}

const nodes = new Map();
let items = [];
function render() {
  const feed = $('feed');
  const ordered = [...items.filter((i) => i.pinned), ...items.filter((i) => !i.pinned)];
  const seen = new Set();
  let cursor = feed.firstElementChild;
  for (const item of ordered) {
    seen.add(item.id);
    let li = nodes.get(item.id);
    if (!li) { li = build(item); nodes.set(item.id, li); }
    paint(li, item);
    if (li === cursor) cursor = cursor.nextElementSibling;
    else feed.insertBefore(li, cursor);
  }
  for (const [id, li] of nodes) {
    if (seen.has(id)) continue;
    li.remove();
    nodes.delete(id);
    opened.delete(id);
    blobs.delete(id);
    const u = objectURLs.get(id);
    if (u) { u.then((x) => URL.revokeObjectURL(x), () => {}); objectURLs.delete(id); }
  }
  $('empty').hidden = items.length > 0;
}

async function load() {
  try {
    const list = (await (await api('/api/items')).json()).items;
    items = list.map(openItem);
    render();
  } catch (e) { fail(e); }
}
setInterval(() => { for (const li of nodes.values()) paint(li, li._item); }, 30000);

// ---- live updates ----
// Server-sent events, read with fetch so the token can travel in a header.

let listening = null;
function stopListening() { if (listening) { listening.abort(); listening = null; } }

function listen() {
  stopListening();
  const ctl = new AbortController();
  listening = ctl;
  (async () => {
    while (!ctl.signal.aborted) {
      try {
        const res = await fetch((cfg.hub || '') + '/api/events', { headers: { Authorization: 'Bearer ' + cfg.token }, signal: ctl.signal });
        if (res.status === 401) { locked(); return; }
        const reader = res.body.getReader();
        const text = new TextDecoder();
        let buf = '';
        for (;;) {
          const { done, value } = await reader.read();
          if (done) break;
          buf += text.decode(value, { stream: true });
          let nl;
          while ((nl = buf.indexOf('\n')) >= 0) {
            const line = buf.slice(0, nl);
            buf = buf.slice(nl + 1);
            if (line.startsWith('data:')) load(); // "hello" too: it catches up after a gap
          }
        }
      } catch { /* dropped: retry below */ }
      if (!ctl.signal.aborted) await new Promise((r) => setTimeout(r, 2000));
    }
  })();
}

// ---- notices, footer ----

function showNotice(text, button, onClick) {
  const n = $('notice');
  n.replaceChildren(text);
  if (button) n.append(el('button', { class: 'btn', type: 'button', text: button, onclick: onClick }));
  n.hidden = false;
}

function paintDevice() { $('rename').textContent = cfg.device; }
$('rename').addEventListener('click', () => {
  if (desktop) { desktop.openSettings(); return; }
  const name = prompt('Name for this device in the feed:', cfg.device);
  if (name && name.trim()) { cfg.device = name.trim().slice(0, 40); store.set('fetch.device', cfg.device); paintDevice(); }
});
$('clear').addEventListener('click', () => {
  const n = items.filter((i) => !i.pinned).length;
  if (n && confirm('Delete ' + n + ' unpinned item' + (n === 1 ? '' : 's') + ' on every device?')) {
    api('/api/items', { method: 'DELETE' }).catch(fail);
  }
});
$('unpair').addEventListener('click', () => {
  if (!confirm("Unpair this browser? It forgets the key; the feed stays on your other devices. You'd need a pairing link to come back.")) return;
  forgetBrowserConfig();
  forgetItems();
  showSetup();
});

async function loadInfo() {
  const info = await (await api('/api/info')).json();
  const h = info.ttl_hours;
  $('policy').textContent = h > 0
    ? 'Unpinned items delete themselves after ' + (h >= 48 ? Math.round(h / 24) + ' days' : Math.round(h) + ' hours')
    : 'Items stay until you delete them';
}

async function start() {
  cfg = loadConfig();
  if (!cfg) {
    if (desktop) { desktop.openSettings(); return; }
    showSetup();
    return;
  }
  $('setup').hidden = true;
  $('app').hidden = false;
  $('notice').hidden = true;
  $('unpairWrap').hidden = !!desktop;
  $('footFp').textContent = C.fingerprint(cfg.key);
  paintDevice();
  fitDraft();
  try {
    const check = await hubCheck(cfg);
    if (check !== C.check(cfg.key)) {
      const msg = check === ''
        ? 'The hub was reset and has no key now.'
        : "This device's key doesn't match the hub's anymore: it was reset from another device.";
      if (desktop) { showNotice(msg + ' Pair this computer again in Settings.', 'Open Settings', () => desktop.openSettings()); return; }
      forgetBrowserConfig();
      showSetup(msg + ' Pair this browser again.');
      return;
    }
    await loadInfo();
  } catch (e) {
    if (e instanceof Locked) { locked(); return; }
    showNotice(e.message + '. Retrying…');
    setTimeout(start, 4000);
    return;
  }
  listen();
}

// The desktop app's "Add a device" opens this page's QR code.
if (desktop && desktop.onAddDevice) desktop.onAddDevice(() => { if (cfg && !$('app').hidden) openSheet(); });

if (/Mac|iPhone|iPad/.test(navigator.userAgent)) $('hint').textContent = $('hint').textContent.replace('Ctrl', '⌘');
start();
})();
