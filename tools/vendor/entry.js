// Audited, dependency-free implementations used by hub/web/fetch-crypto.js.
// They work where the browser's own crypto (crypto.subtle) is switched off,
// which is any page served over plain http, like a hub on a home network.
import { gcm } from '@noble/ciphers/aes';
import { hkdf } from '@noble/hashes/hkdf';
import { sha256 } from '@noble/hashes/sha2';
import qrcode from 'qrcode-generator';

globalThis.FetchVendor = { gcm, hkdf, sha256, qrcode };
