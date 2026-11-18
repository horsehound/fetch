'use strict';
// A bare Electron app with no windows of its own, for test/web.e2e.js: it
// opens the hub's web page in plain windows, the way a browser would.
const { app } = require('electron');

app.on('window-all-closed', () => { /* stay up until the test closes the app */ });
