'use strict';
const { contextBridge, ipcRenderer } = require('electron');

contextBridge.exposeInMainWorld('fetchSettings', {
  get: () => ipcRenderer.invoke('settings:get'),
  pair: (link) => ipcRenderer.invoke('settings:pair', link),
  setUp: (url, token, reset) => ipcRenderer.invoke('settings:set-up', url, token, reset),
  unpair: () => ipcRenderer.invoke('settings:unpair'),
  addDevice: () => ipcRenderer.send('settings:add-device'),
  save: (draft) => ipcRenderer.invoke('settings:save', draft),
  recording: (on) => ipcRenderer.send('settings:recording', on),
});
