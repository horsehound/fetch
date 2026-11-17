'use strict';
// What the built-in page gets from the app: its settings (hub, token, key,
// device name) and a way back to the settings window.
const { contextBridge, ipcRenderer } = require('electron');

contextBridge.exposeInMainWorld('fetchDesktop', {
  config: () => ipcRenderer.sendSync('page:config'),
  openSettings: () => ipcRenderer.send('page:open-settings'),
  setKey: (key) => ipcRenderer.invoke('page:set-key', key),
  onAddDevice: (fn) => ipcRenderer.on('page:add-device', () => fn()),
});
