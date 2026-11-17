'use strict';
(() => {
  const api = window.fetchQuick;
  const $ = (id) => document.getElementById(id);
  const text = $('text');
  const list = $('files');

  let files = [];            // { file, url } for everything attached
  const inFlight = new Map(); // throws the app is still uploading, by id

  // ---- the draft ----
  function fit() {
    text.style.height = 'auto';
    text.style.height = text.scrollHeight + 'px';
    const empty = !text.value.trim() && files.length === 0;
    $('throw').disabled = empty;
    $('hint').classList.toggle('off', empty);
  }

  function size(n) {
    if (n < 1000) return n + ' B';
    if (n < 1e6) return (n / 1e3).toFixed(n < 1e4 ? 1 : 0) + ' kB';
    return (n / 1e6).toFixed(n < 1e7 ? 1 : 0) + ' MB';
  }

  function renderFiles() {
    list.replaceChildren(...files.map((entry) => {
      const li = document.createElement('li');
      li.className = 'file';
      if (entry.url) {
        const img = document.createElement('img');
        img.src = entry.url;
        img.alt = '';
        li.append(img);
      } else {
        const ext = document.createElement('span');
        ext.className = 'ext';
        ext.textContent = (entry.file.name.includes('.') ? entry.file.name.split('.').pop() : 'file').slice(0, 4);
        li.append(ext);
      }
      const label = document.createElement('span');
      label.style.minWidth = '0';
      const name = document.createElement('div');
      name.className = 'name';
      name.textContent = entry.url && entry.file.name === 'image.png' ? 'Picture' : entry.file.name;
      const bytes = document.createElement('div');
      bytes.className = 'size';
      bytes.textContent = size(entry.file.size);
      label.append(name, bytes);
      const remove = document.createElement('button');
      remove.className = 'remove';
      remove.type = 'button';
      remove.textContent = '×';
      remove.setAttribute('aria-label', 'Remove ' + name.textContent);
      remove.addEventListener('click', () => {
        files = files.filter((f) => f !== entry);
        renderFiles();
        text.focus();
      });
      li.append(label, remove);
      return li;
    }));
    fit();
  }

  function attach(file) {
    const url = file.type.startsWith('image/') ? URL.createObjectURL(file) : '';
    files.push({ file, url });
  }

  function setDraft(draft) {
    text.value = draft.text;
    files = draft.files;
    renderFiles();
  }

  // ---- throwing ----
  async function throwIt() {
    if ($('throw').disabled) return;
    const draft = { text: text.value, files };
    const id = String(Date.now()) + Math.random().toString(16).slice(2);
    const payload = { id, text: draft.text, files: [] };
    for (const { file } of draft.files) {
      const path = api.pathFor(file);
      payload.files.push(path
        ? { path }
        : { name: file.name, type: file.type, bytes: await file.arrayBuffer() });
    }
    inFlight.set(id, draft);   // kept until the app confirms, in case the upload fails
    setDraft({ text: '', files: [] });
    api.throw(payload);        // the app hides this window and uploads in the background
  }

  api.onThrown((id) => {
    const draft = inFlight.get(id);
    inFlight.delete(id);
    if (draft) draft.files.forEach((f) => f.url && URL.revokeObjectURL(f.url));
  });

  // The upload failed: put the draft back, ahead of anything typed since.
  api.onFailed((id) => {
    const draft = inFlight.get(id);
    inFlight.delete(id);
    if (!draft) return;
    setDraft({
      text: [draft.text, text.value].filter((t) => t.trim()).join('\n'),
      files: [...draft.files, ...files],
    });
  });

  // ---- input ----
  text.addEventListener('input', fit);
  text.addEventListener('keydown', (e) => {
    if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) {
      e.preventDefault();
      throwIt();
    }
  });
  document.addEventListener('keydown', (e) => {
    if (e.key === 'Escape') api.hide();
  });
  document.addEventListener('paste', (e) => {
    const pasted = [...(e.clipboardData ? e.clipboardData.files : [])];
    if (!pasted.length) return; // plain text lands in the box as usual
    e.preventDefault();
    pasted.forEach(attach);
    renderFiles();
  });
  // A click on the transparent area around the card closes the box.
  document.body.addEventListener('mousedown', (e) => {
    if (e.target === document.body) api.hide();
  });

  $('throw').addEventListener('click', throwIt);
  $('open').addEventListener('click', () => api.openFull());
  $('settings').addEventListener('click', () => api.openSettings());

  api.onShown(() => {
    const card = $('card');
    card.classList.remove('pop');
    void card.offsetWidth; // restart the entrance animation
    card.classList.add('pop');
    text.focus();
    fit();
  });

  fit();
})();
