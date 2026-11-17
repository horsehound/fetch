'use strict';
const { contextBridge, ipcRenderer, webUtils } = require('electron');

contextBridge.exposeInMainWorld('fetchQuick', {
  hide: () => ipcRenderer.send('quick:hide'),
  openFull: () => ipcRenderer.send('quick:open-full'),
  openSettings: () => ipcRenderer.send('quick:open-settings'),
  throw: (payload) => ipcRenderer.send('quick:throw', payload),
  // Where a pasted file lives on disk; empty for clipboard pictures.
  pathFor: (file) => webUtils.getPathForFile(file),
  onShown: (fn) => ipcRenderer.on('quick:shown', () => fn()),
  onThrown: (fn) => ipcRenderer.on('quick:thrown', (_e, id) => fn(id)),
  onFailed: (fn) => ipcRenderer.on('quick:failed', (_e, id) => fn(id)),
});
