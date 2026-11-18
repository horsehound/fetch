# Fetch encryption, format version 1

Everything a device sends is encrypted on that device. The hub stores and relays opaque bytes and never holds the key. This document is the reference for every implementation; `vectors.json` holds test vectors produced by the reference implementation in `hub/seal.go`.

## What the hub can and can't see

The hub sees, for each item: when it was uploaded, how many bytes of encrypted data it has (zero for text), and whether it is pinned. It also sees which IP address talks to it, and when.

The hub can't see: the text, the pictures, file contents, file names, file types, whether an item is text, a picture or a file, or which device sent it.

A hub that misbehaves can delete items, withhold them, or serve an old item again. It can't read, change or forge one: any change makes decryption fail.

## The key

One 32-byte random key, created by the first device, shared by all paired devices. It is never derived from a password.

Text form (in pairing links): base64url without padding, 43 characters.

**Check.** `check = HKDF-SHA256(ikm = key, salt = none, info = "fetch v1 check", length = 16)`, as 32 lowercase hex characters. The hub stores the check of the first device's key (`PUT /api/vault`), and every device compares its own check with the hub's (`GET /api/vault`) to know that it has the same key as the others. "Salt = none" means 32 zero bytes, as in RFC 5869. Devices may show the first 8 hex characters, uppercased as `ABCD-EF01`, as a fingerprint people can compare by eye.

## Pairing links

```
fetchpair://v1?hub=<hub address>&token=<access token>&key=<key>
```

Query values are percent-encoded; parameter order doesn't matter. A hub address without a scheme gets `http://` if it looks like a home-network address (an IP, a port, a bare name or `.local`/`.lan`) and `https://` otherwise. The link holds everything needed to read and change the feed, so it is shown as a QR code or copied once and treated like a password.

## An item

Each item gets a fresh random 16-byte salt. Two keys come from it:

```
meta_key = HKDF-SHA256(key, salt, "fetch v1 meta", 32)
data_key = HKDF-SHA256(key, salt, "fetch v1 data", 32)
```

### Metadata envelope

```
envelope = 0x01 || salt (16) || AES-256-GCM(meta_key, nonce = 12 zero bytes, metadata JSON)
```

The ciphertext includes the 16-byte tag at its end. A zero nonce is safe because every envelope has its own key. There is no associated data.

The metadata is a UTF-8 JSON object:

| Field | | |
|---|---|---|
| `v` | number | 1 |
| `kind` | string | `text`, `image` or `file` |
| `device` | string | name of the sending device, up to 40 characters |
| `text` | string | text items: the text itself, at most 256 KiB of UTF-8. Longer text is sent as a `.txt` file |
| `name` | string | images and files: the file name |
| `mime` | string | images and files: the media type |
| `size` | number | images and files: plaintext size of the data, if known |

Readers ignore fields they don't know. `image` is used only for `image/png`, `image/jpeg`, `image/gif`, `image/webp`, `image/avif` and `image/bmp`; any other picture (HEIC, SVG) is converted or sent as a `file`.

### Data

Images and files carry data; text items carry none (zero bytes). Data is the plaintext split into chunks of 65536 bytes (the last may be shorter, and is empty only when the whole file is), each encrypted on its own:

```
chunk_i = AES-256-GCM(data_key, nonce_i, plaintext chunk i)        (65536 + 16 bytes, the last up to that)
nonce_i = i as an 11-byte big-endian number || (0x01 for the last chunk, 0x00 otherwise)
data    = chunk_0 || chunk_1 || ... || chunk_n
```

A reader knows a chunk is the last one when the data ends right after it (so it reads up to 65553 bytes before deciding). Data that ends early, has extra bytes, or has chunks swapped, repeated or dropped fails to decrypt. A plaintext of n bytes becomes `n + 16 * max(1, ceil(n / 65536))` bytes.

Because `data_key` comes from the envelope's salt, data can only be decrypted together with its own envelope.

## The hub's API

All calls need `Authorization: Bearer <token>`.

| | |
|---|---|
| `POST /api/items` | `Content-Type: application/x-fetch-sealed`. Body: envelope length as 4 bytes big-endian, the envelope, then the data. The hub checks only the shape: the envelope starts with `0x01` and is 33 bytes to 2 MiB, the data size is possible. Anything else is refused (`415` for other content types). |
| `GET /api/items` | `{"items": [{"id", "meta": <envelope, base64>, "size": <data bytes>, "created": <unix ms>, "pinned"}]}`, newest first |
| `GET /api/items/{id}/raw` | the encrypted data |
| `PUT /api/items/{id}/pin?on=1` / `on=0` | pin or unpin |
| `DELETE /api/items/{id}` | delete one item |
| `DELETE /api/items` | delete everything unpinned |
| `GET /api/vault` | `{"check": "<hex>"}`, or an empty string if no device has set up a key yet |
| `PUT /api/vault` | `{"check": "<hex>"}`: the first device claims the hub. `409` if another key's check is already there |
| `DELETE /api/vault` | start over: deletes every item, pinned too, and the check |
| `GET /api/events` | server-sent events; any `data:` line other than `hello` means the feed changed |
| `GET /api/info` | `{"version", "format": 1, "ttl_hours", "max_mb"}` |

## Setting up a device

1. **With a pairing link**: parse it, `GET /api/vault`. If the hub's check equals the key's, done. If the hub has no check (it was reset), `PUT` it. Otherwise the link is stale: say so.
2. **First device**: the user enters the hub address and token. If `GET /api/vault` has no check, create a key and `PUT` its check. If it has one, this hub is already set up: pair from a device that is.
3. **Start over** (any set-up device, after a clear warning): `DELETE /api/vault`, create a new key, `PUT` its check. Every other device has to pair again.

Devices check the vault again whenever they connect, so a device whose key no longer matches (the hub was reset elsewhere) can say so instead of showing nothing.
