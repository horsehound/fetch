'use strict';
(async () => {
  const api = window.fetchSettings;
  const $ = (id) => document.getElementById(id);
  const current = await api.get();
  const mac = current.platform === 'darwin';

  $('url').value = current.url;
  $('token').value = current.token;
  $('device').value = current.device;
  $('openAtLogin').checked = current.openAtLogin;

  // ---- shortcut field ----
  let hotkey = current.hotkey;
  const field = $('hotkey');
  const pretty = (accel) => (accel
    ? accel.split('+').map((part) => (mac ? { Control: '⌃', Alt: '⌥', Shift: '⇧', Command: '⌘' }[part] || part : part.replace('Control', 'Ctrl'))).join(mac ? ' ' : ' + ')
    : 'None');
  const showHotkey = () => { field.value = pretty(hotkey); };
  showHotkey();
  if (current.hotkey && !current.hotkeyActive) {
    $('hotkeyHelp').textContent = `${pretty(current.hotkey)} is taken by another app or blocked by the system. Pick another, or bind your own system shortcut to this app with --throw added.`;
  }

  // Turns a key press into the name Electron uses, or '' for keys that can't be a shortcut.
  function keyName(e) {
    const c = e.code;
    if (/^Key[A-Z]$/.test(c)) return c.slice(3);
    if (/^Digit[0-9]$/.test(c)) return c.slice(5);
    if (/^F([1-9]|1[0-9]|2[0-4])$/.test(c)) return c;
    return {
      Space: 'Space', Enter: 'Return', Tab: 'Tab', Backquote: '`', Minus: '-', Equal: '=',
      BracketLeft: '[', BracketRight: ']', Semicolon: ';', Quote: "'", Comma: ',', Period: '.', Slash: '/', Backslash: '\\',
      ArrowUp: 'Up', ArrowDown: 'Down', ArrowLeft: 'Left', ArrowRight: 'Right',
      Home: 'Home', End: 'End', PageUp: 'PageUp', PageDown: 'PageDown', Insert: 'Insert', Delete: 'Delete',
    }[c] || '';
  }

  field.addEventListener('focus', () => {
    field.classList.add('recording');
    field.value = 'Press keys…';
    api.recording(true); // so the current shortcut doesn't fire while you press it
  });
  field.addEventListener('blur', () => {
    field.classList.remove('recording');
    api.recording(false);
    showHotkey();
  });
  field.addEventListener('keydown', (e) => {
    if (e.key === 'Tab') return; // keep keyboard navigation working
    e.preventDefault();
    if (e.key === 'Escape') { field.blur(); return; }
    const mods = [];
    if (e.ctrlKey) mods.push('Control');
    if (e.altKey) mods.push('Alt');
    if (e.shiftKey) mods.push('Shift');
    if (e.metaKey) mods.push(mac ? 'Command' : 'Super');
    const key = keyName(e);
    if (!key) return; // still holding only modifiers
    const functionKey = /^F\d+$/.test(key);
    if (!mods.length && !functionKey) {
      $('hotkeyHelp').textContent = 'Add Ctrl, Alt or another modifier so it doesn\'t clash with typing.';
      return;
    }
    hotkey = [...mods, key].join('+');
    $('hotkeyHelp').textContent = 'Click the field and press the keys you want.';
    field.blur();
  });
  $('clearHotkey').addEventListener('click', () => { hotkey = ''; showHotkey(); });

  // ---- saving ----
  async function save(force) {
    for (const id of ['url', 'token', 'hotkey']) $(id).classList.remove('bad');
    $('error').textContent = '';
    $('force').hidden = true;
    $('save').disabled = true;
    $('save').textContent = 'Checking…';
    const result = await api.save({
      url: $('url').value,
      token: $('token').value,
      device: $('device').value,
      hotkey,
      openAtLogin: $('openAtLogin').checked,
    }, force);
    $('save').disabled = false;
    $('save').textContent = 'Save';
    if (result.ok) return; // the app closes this window
    $('error').textContent = result.message;
    if (result.field) $(result.field).classList.add('bad');
    $('force').hidden = !result.canForce;
  }
  $('form').addEventListener('submit', (e) => { e.preventDefault(); save(false); });
  $('force').addEventListener('click', () => save(true));
})();
