# Fetch

Move text, pictures and files between your iPhone, your Linux laptop and your Windows PC. One feed, live on every device, that cleans up after itself.

Three parts:

- **Hub** (`hub/`): one small program you host yourself, on a Proxmox container at home or a small VPS. It stores the feed and serves the web page.
- **Web page**: what the laptop and the PC use. Open the hub's address in any browser. Nothing to install.
- **iPhone app** (`Sources/`, `Share/`): the feed, a Paste button, a photo picker, and "Fetch" in the share sheet.

Unpinned items delete themselves after 7 days. Pin what you want to keep.

## 1. Run the hub

The hub is a single file with no dependencies. Pick whichever fits where it will live. Each way ends with the hub printing its address and an access token; that pair is all a device needs.

### On your home network (Proxmox LXC, NAS, spare machine)

Download `fetch-linux-amd64` from the latest [release](../../releases), then:

```sh
install -m 755 fetch-linux-amd64 /usr/local/bin/fetch
cp fetch-hub.service /etc/systemd/system/      # the file is in hub/ in this repo
systemctl daemon-reload && systemctl enable --now fetch-hub
journalctl -u fetch-hub | grep -E 'open|token'
```

Devices reach it at `http://<its-ip>:8787` while they are on your network (or on a VPN into it).

### On a VPS, reachable from anywhere

On the public internet the hub has to be behind https, otherwise the token travels in the clear. Point a domain at the server and let Caddy fetch the certificate:

```sh
git clone https://github.com/horsehound/fetch && cd fetch
FETCH_DOMAIN=fetch.example.com docker compose -f docker-compose.vps.yml up -d
docker compose -f docker-compose.vps.yml logs hub | grep token
```

Devices then use `https://fetch.example.com`, at home or away, no VPN.

Without Docker: run the binary as above with `-addr 127.0.0.1:8787` and put any reverse proxy in front (`caddy reverse-proxy --from fetch.example.com --to localhost:8787`), or hand the hub a certificate directly with `-cert` and `-key`.

### With Docker at home

```sh
docker compose up -d
docker compose logs hub | grep token
```

### Options

| Option | Default | |
|---|---|---|
| `-addr` / `FETCH_ADDR` | `:8787` | where to listen |
| `-data` / `FETCH_DATA` | `./data` | where the feed is stored |
| `-token` / `FETCH_TOKEN` | generated | your own token, at least 12 characters |
| `-ttl` / `FETCH_TTL` | `168h` | how long unpinned items live; `0` keeps everything |
| `-max-mb` | `500` | largest upload |
| `-cert`, `-key` / `FETCH_CERT`, `FETCH_KEY` | none | serve https directly with this certificate |

The token is the only lock on the feed: anyone who has the address and the token can read and delete everything in it.

## 2. Laptop and PC

Open `http://<hub-address>:8787`, enter the token once, and keep the tab pinned.

- **Ctrl+V anywhere** on the page sends what's on your clipboard: text, a screenshot, a copied image.
- **Drag files** onto the page to send them.
- **Click a text item** to copy it. **Save** downloads a picture or file.

Copying a *picture* straight to the clipboard needs the page to be served over https; browsers don't allow it on plain http. Everything else works either way. With the VPS setup above, or any hub behind https, the Copy button appears on pictures.

### From the terminal (optional)

The same binary is a command-line client. `fetch-windows-amd64.exe` is in the release for the PC.

```sh
fetch login http://<hub-address>:8787 <token>
fetch send screenshot.png notes.pdf
fetch send -t "some text"
fetch latest                      # prints the newest text
```

That makes clipboard hotkeys easy to bind in your desktop's shortcut settings:

```sh
wl-paste | fetch send             # Linux (Wayland): send clipboard, text or image
fetch latest | wl-copy            # Linux: newest text into the clipboard
```

```powershell
fetch send -t (Get-Clipboard -Raw)   # Windows: send clipboard text
```

## 3. iPhone

The app isn't on the App Store. Every push to this repo builds it with GitHub Actions and publishes `Fetch.ipa` in the [releases](../../releases); you install that file with [SideStore](https://sidestore.io) or AltStore, which sign it with your own Apple ID. No Mac needed.

1. Set up SideStore on the iPhone if you haven't (its site walks through it).
2. On the iPhone, download `Fetch.ipa` from the latest [release](../../releases).
3. In SideStore, open **My Apps**, tap **+**, and pick the file.
4. When it asks about app extensions, **keep them**. The extension is what puts Fetch in the share sheet. It uses one extra App ID on a free Apple account.
5. Open Fetch and enter the hub address and token (`192.168.1.20:8787` or `fetch.example.com`; the app adds http or https itself).

To update, install the newer `Fetch.ipa` the same way; your settings stay.

With a free Apple ID, sideloaded apps count against Apple's limit of three and have to be refreshed every seven days, which SideStore does for you.

Using it:

- **Paste** sends the clipboard (text, link or picture) without the iOS permission prompt.
- **Photos** sends up to 20 pictures at once. HEIC photos are converted to JPEG so the other devices can open them.
- **Tap text** to copy it. **Tap a picture** to copy it; the arrow saves it to Photos.
- **Share sheet → Fetch** from Photos, Safari, Files or any other app.
- Swipe left to delete, right to pin.

The phone has to be able to reach the hub. A hub on your home network needs the same Wi-Fi, or a VPN such as Tailscale or WireGuard when you're out. A hub on a VPS with a domain works from anywhere.

## API

Everything is plain HTTP with `Authorization: Bearer <token>`.

| | |
|---|---|
| `GET /api/items` | the feed, newest first |
| `POST /api/items` | `text/plain` body → text; any other body (optional `?name=`) → picture or file; `multipart/form-data` also works |
| `GET /api/items/{id}/raw` | the content (`?dl=1` forces a download) |
| `PUT /api/items/{id}/pin?on=1` | pin or unpin (`on=0`) |
| `DELETE /api/items/{id}` | delete one |
| `DELETE /api/items` | delete everything unpinned |
| `GET /api/events` | server-sent events; a `data:` line means the feed changed |

```sh
curl -H "Authorization: Bearer $TOKEN" --data-binary @photo.jpg "http://hub:8787/api/items?name=photo.jpg"
```

## Building it yourself

```sh
cd hub && go test ./... && go build -o fetch .
```

The app is built by `.github/workflows/build.yml` on a macOS runner: `xcodegen generate` creates the Xcode project from `project.yml`, and the result is packaged unsigned for SideStore. Fork the repo and the same workflow builds your own copy.
