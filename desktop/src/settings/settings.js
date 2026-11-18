'use strict';
(async () => {
  const api = window.fetchSettings;
  const $ = (id) => document.getElementById(id);
  let current = await api.get();
  const mac = current.platform === 'darwin';

  $('device').value = current.device;
  $('openAtLogin').checked = current.openAtLogin;

  // ---- the hub: paired, or pairing ----
  function showHub(view) {
    current = view;
    $('paired').hidden = !view.paired;
    $('unpaired').hidden = view.paired;
    $('hubName').textContent = view.url;
    $('fp').textContent = view.fingerprint;
    $('hubError').textContent = '';
    $('reset').hidden = true;
    if (!view.paired) $('link').focus();
  }
  showHub(current);

  async function busy(button, label, work) {
    const was = button.textContent;
    button.disabled = true;
    button.textContent = label;
    $('hubError').textContent = '';
    try { return await work(); } finally { button.disabled = false; button.textContent = was; }
  }

  function outcome(result) {
    if (result.ok) { showHub(result.view); return; }
    $('hubError').textContent = result.message;
    $('reset').hidden = !result.canReset;
  }

  $('pair').addEventListener('click', () => busy($('pair'), 'Pairing…', async () => {
    if (!$('link').value.trim()) { $('hubError').textContent = 'Paste the pairing link first.'; return; }
    outcome(await api.pair($('link').value));
    if (current.paired) $('link').value = '';
  }));
  $('link').addEventListener('keydown', (e) => { if (e.key === 'Enter') { e.preventDefault(); $('pair').click(); } });

  $('setUp').addEventListener('click', () => busy($('setUp'), 'Setting up…', async () => {
    outcome(await api.setUp($('url').value, $('token').value, false));
  }));
  $('reset').addEventListener('click', () => {
    if (!confirm('Delete everything on the hub, pinned items too, and start over with a new key? Every other device has to be paired again.')) return;
    busy($('reset'), 'Starting over…', async () => outcome(await api.setUp($('url').value, $('token').value, true)));
  });

  $('addDevice').addEventListener('click', () => api.addDevice());
  $('unpair').addEventListener('click', async () => {
    if (!confirm("Unpair this computer? It forgets the key. The feed stays on your other devices; you'd need a pairing link to come back.")) return;
    showHub(await api.unpair());
  });

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

  // ---- saving this computer's settings ----
  $('form').addEventListener('submit', async (e) => {
    e.preventDefault();
    $('hotkey').classList.remove('bad');
    $('error').textContent = '';
    $('save').disabled = true;
    const result = await api.save({ device: $('device').value, hotkey, openAtLogin: $('openAtLogin').checked });
    $('save').disabled = false;
    if (result.ok) return; // the app closes this window
    $('error').textContent = result.message;
    if (result.field) $(result.field).classList.add('bad');
  });
})();
