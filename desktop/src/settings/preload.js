'use strict';
const { contextBridge, ipcRenderer } = require('electron');

contextBridge.exposeInMainWorld('fetchSettings', {
  get: () => ipcRenderer.invoke('settings:get'),
  save: (draft, force) => ipcRenderer.invoke('settings:save', draft, force),
  recording: (on) => ipcRenderer.send('settings:recording', on),
});
