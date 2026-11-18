<p align="center">
  <img src=".github/logo.png" width="128" height="128" alt="Fetch logo: a tennis ball in flight">
</p>

<h1 align="center">Fetch</h1>

<p align="center">
  Move text, pictures and files between your iPhone or Android phone and your Windows, macOS and Linux computers.<br>
  One feed, live on every device, that cleans up after itself. You host it yourself,<br>
  and can give friends a space of their own on it that you can't read.
</p>

The parts:

- **Hub** (`hub/`): one small program you host yourself, on a Proxmox container at home or a small VPS. It stores the feed (encrypted, without the key) and serves the web page.
- **Desktop app** (`desktop/`): for Windows, macOS and Linux. A hotkey pops up a small Throw box; paste, hit Throw, and it uploads in the background. Its full window is the web page.
- **Web page**: the same feed in any browser. Nothing to install.
- **iPhone app** (`Sources/`, `Share/`): the feed, a Paste button, a photo picker, and "Fetch" in the share sheet.
- **Android app** (`android/`): the same app for Android phones.

Unpinned items delete themselves after 7 days. Pin what you want to keep.

Shell commands and code are recognised, with their language, and shown as highlighted code blocks on every device.

**End-to-end encrypted.** Everything is encrypted on the device that sends it and decrypted on the one that shows it: text, pictures, files, their names and types, even which device sent them. The key lives only on your devices; the hub stores scrambled data and can't read it. Devices join by pairing (a QR code or a link), see [Pairing](#2-pair-your-devices).

## 1. Run the hub

The hub is a single file with no dependencies. Pick whichever fits where it will live. Each way ends with the hub printing its address and an access token; the first device needs both (see [Pairing](#2-pair-your-devices)).

### On your home network (Proxmox LXC, NAS, spare machine)

On any Linux with systemd (a Debian or Ubuntu LXC on Proxmox is ideal), as root:

```sh
curl -fsSL https://raw.githubusercontent.com/horsehound/fetch/main/hub/install.sh | sh
```

It installs the newest release (checked against its published checksums), starts the `fetch-hub` service, prints the address and token, and turns on daily automatic updates (see [Updating](#updating-the-hub)). By hand instead: download `fetch-linux-amd64` (or `-arm64`) from the latest [release](../../releases), `install -m 755` it as `/usr/local/bin/fetch`, copy `hub/fetch-hub.service` to `/etc/systemd/system/`, and `systemctl enable --now fetch-hub`.

Devices reach it at `http://<its-ip>:8787` while they are on your network (or on a VPN into it).

### On a VPS, reachable from anywhere

On the public internet the hub has to be behind https, otherwise the token travels in the clear. Point a domain at the server and let Caddy fetch the certificate:

```sh
git clone https://github.com/horsehound/fetch && cd fetch
FETCH_DOMAIN=fetch.example.com docker compose -f docker-compose.vps.yml up -d
docker compose -f docker-compose.vps.yml logs hub | grep token
```

Devices then use `https://fetch.example.com`, at home or away, no VPN.

Without Docker: install as above, change the service to `-addr 127.0.0.1:8787` (`systemctl edit --full fetch-hub`), and put any reverse proxy in front (`caddy reverse-proxy --from fetch.example.com --to localhost:8787`), or hand the hub a certificate directly with `-cert` and `-key`.

### With Docker at home

```sh
curl -fsSLO https://raw.githubusercontent.com/horsehound/fetch/main/docker-compose.yml
docker compose up -d
docker compose logs hub | grep token
```

The image (`ghcr.io/horsehound/fetch`, for amd64 and arm64) comes with every release.

### Options

| Option | Default | |
|---|---|---|
| `-addr` / `FETCH_ADDR` | `:8787` | where to listen |
| `-data` / `FETCH_DATA` | `./data` | where the feed is stored |
| `-token` / `FETCH_TOKEN` | generated | your own token, at least 12 characters |
| `-ttl` / `FETCH_TTL` | `168h` | how long unpinned items live; `0` keeps everything |
| `-max-mb` / `FETCH_MAX_MB` | `500` | largest upload |
| `-mode` / `FETCH_MODE` | `invite` | who else may make a space here: `invite` (with a code from you), `public` (anyone) or `private` (nobody); see [Sharing your hub](#sharing-your-hub) |
| `-space-mb` / `FETCH_SPACE_MB` | `500` | storage for each space you hand out; `0` for no limit. Your own feed has none |
| `-max-spaces` / `FETCH_MAX_SPACES` | `50` | most spaces besides your own |
| `-space-idle` / `FETCH_SPACE_IDLE` | `2160h` (90 days) | delete a space nobody has used for this long; `0` never. Never applies to your own |
| `-trust-proxy` / `FETCH_TRUST_PROXY` | off | behind a reverse proxy: take visitors' addresses from `X-Forwarded-For` (for the public mode's limit per address) |
| `-cert`, `-key` / `FETCH_CERT`, `FETCH_KEY` | none | serve https directly with this certificate |

The token decides who may use the hub. It can't read anything: whoever has the token but not the key can delete or withhold items, but not read, change or fake them. Keep both private anyway.

### Sharing your hub

Other people can use your hub without seeing your feed, and without you seeing theirs. Each person gets a **space**: a feed of their own with its own token, encrypted with a key that only their devices have. There are no accounts; the pairing link is all a space needs.

- **Invite** (the default): open **Invite someone** at the bottom of the web page or the desktop app's window, or run `fetch invite` on a computer paired with your own feed. Send them the link it gives you (`https://your-hub/#invite=…`). Opened in a browser it makes their space right there; the apps and `fetch pair` take it too. A link works once, for 7 days (`fetch invite -days 30 -mb 2000` for longer or bigger).
- **Public**: anyone who finds the hub can make a space (a few per address per day). Behind a reverse proxy, set `FETCH_TRUST_PROXY=1` so that limit sees their real address; `docker-compose.vps.yml` does.
- **Private**: just your own feed, as before.

Each space holds up to `-space-mb` (500 MB); when it's full, sending says so until something is deleted or expires. Spaces nobody has used for 90 days are deleted.

What you can see as the host: how many spaces there are, how much each stores, how many items, and when it was made and last used (`fetch spaces`). Not what's in them: text, names, pictures and files are all encrypted with keys you don't have. `fetch spaces delete <id>` deletes one. Someone can delete their own space under **Delete my space** in their browser.

Your own feed is the one the printed token opens. Nothing about it changes when you share the hub.

### Updating the hub

Every push to this repo makes a release. How the hub gets it depends on how you installed it:

| Installed with | Update |
|---|---|
| `install.sh` | nothing to do: it checks once a day and updates itself. Right now: `fetch update`. Turn the daily check off with `systemctl disable --now fetch-hub-update.timer` |
| the binary by hand | `sudo fetch update` (installs the newest release, checks its checksum, restarts `fetch-hub`). `fetch update -check` only looks |
| Docker | `docker compose pull && docker compose up -d` (add `-f docker-compose.vps.yml` for the VPS setup) |

Your items, the token and the key's check stay where they are; devices reconnect by themselves. A hub from before `fetch update` existed updates once by running `install.sh` (above); it keeps your service settings and data.

Forks: releases come from the repository that built them, so a fork's hub updates from the fork. Set `FETCH_REPO=you/fetch` for `install.sh`, and `FETCH_IMAGE=ghcr.io/you/fetch:latest` for Docker.

## 2. Pair your devices

The first device creates the key; every other device gets it from one that already has it. The key never touches the hub.

1. **The first device**: install Fetch on it (see below), and in the first-device part of its settings enter the hub's address and token, then **Set up**. On someone else's hub, use the invite link they sent you instead (paste it where a pairing link goes, or just open it in a browser), or the invite code as the token. On a public hub, leave the token empty.
2. **Every other device:** on a device that's set up, open **Add a device**. It shows a QR code and a pairing link.
   - iPhone: scan the QR code with the Camera app; it opens Fetch, which asks to pair.
   - Android: **Settings → Scan code** in Fetch.
   - Computers and browsers: copy the link (or have it sent to you) and paste it under **Pairing link**.

The easiest order: set up a computer first, then scan its code with the phones. Every paired device shows the same key fingerprint (like `3F9A-0C21`), so you can check at a glance that they match.

The pairing link holds the address, the token and the key: it opens everything you send. Show it to your own devices, don't post it.

**Start over** (in the same settings) deletes everything on the hub, pinned items too, and makes a new key; every other device then has to pair again. That's also the way out if you lose all your devices at once: without the key nobody can read the old items, you included.

A hub from before encryption deletes its old, unencrypted items when it is updated.

## 3. Laptop and PC

### Desktop app

Download the build for your system from the latest [release](../../releases/latest):

| System | File | Install |
|---|---|---|
| Windows | `Fetch-desktop-win-x64.exe` | Run it. Windows warns about an unknown publisher: **More info → Run anyway**. |
| macOS | `Fetch-desktop-mac-arm64.dmg` (Apple silicon) or `-x64.dmg` (Intel) | Drag Fetch to Applications. On first start macOS blocks it: **System Settings → Privacy & Security → Open Anyway**. |
| Arch, CachyOS | `Fetch-desktop-linux-x64.pacman` | `sudo pacman -U Fetch-desktop-linux-x64.pacman` |
| Debian, Ubuntu | `Fetch-desktop-linux-amd64.deb` | `sudo apt install ./Fetch-desktop-linux-amd64.deb` |
| Other Linux | `Fetch-desktop-linux-x86_64.AppImage` | `chmod +x` it and run it. |

The warnings on Windows and macOS are there because the builds aren't signed with a paid developer certificate.

On first start, Settings opens: paste a pairing link, or set up the hub if this is the first device. After that the app sits in the tray:

- **Ctrl+Alt+F** (changeable in Settings) opens the **Throw** box wherever you are. Type or paste text, pictures or files, press **Enter** or **Throw**. The box closes at once and the upload runs in the background. If the hub can't be reached you get a notification and your draft is back in the box.
- **Open Fetch** in the box or the tray opens the full window: the feed, live, exactly as in the browser. Here the Copy button works on pictures even when the hub is plain http.
- **Esc** or a click outside closes the box and keeps what you typed.
- Settings also has "Start Fetch when I log in" and **Add a device…**.

**Linux on Wayland:** whether an app may claim a global shortcut is up to the desktop. If Ctrl+Alt+F does nothing, add a custom shortcut in your system's keyboard settings that runs `fetch-desktop --throw` (for the AppImage: the path to the file, then `--throw`). That works everywhere.

The app has been driven end to end on Linux. The Windows and macOS builds come out of the same code and the same build, but nobody has tried them on a real machine yet; please open an issue if something is off.

### In a browser

Open `http://<hub-address>:8787`, paste a pairing link once (or set up the hub there), and keep the tab pinned. The key stays in that browser.

- **Ctrl+V anywhere** on the page sends what's on your clipboard: text, a screenshot, a copied image.
- **Drag files** onto the page to send them.
- **Click a text item** to copy it. **Save** downloads a picture or file.

Copying a *picture* straight to the clipboard needs the page to be served over https; browsers don't allow it on plain http. Everything else works either way, encryption included. With the VPS setup above, any hub behind https, or the desktop app, the Copy button appears on pictures.

One thing to know about the browser: the page itself, with its encryption code, comes from the hub each time you open it. If someone took over your hub, they could hand your browser a changed page that sends the key away. The apps don't have that weak spot: they carry their own code. On a hub you run yourself that's a small risk, but if you'd rather not take it, use the apps.

### From the terminal (optional)

The same binary is a command-line client. `fetch-windows-amd64.exe` is in the release for the PC.

```sh
fetch pair '<pairing link>'       # or: fetch init <hub-address> <token> on the first device
fetch link                        # the pairing link for another device
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

## 4. iPhone

The app isn't on the App Store. Every push to this repo builds it with GitHub Actions and publishes `Fetch.ipa` in the [releases](../../releases); you install that file with [SideStore](https://sidestore.io) or AltStore, which sign it with your own Apple ID. No Mac needed.

1. Set up SideStore on the iPhone if you haven't (its site walks through it).
2. In SideStore, open **Sources**, add this source, and install Fetch from it:

   ```
   https://raw.githubusercontent.com/horsehound/houndapps/main/source.json
   ```

   Updates then show up in SideStore by themselves. Without the source: download `Fetch.ipa` from the latest [release](../../releases), open **My Apps**, tap **+**, and pick the file.
3. When it asks about app extensions, **keep them**. The extension is what puts Fetch in the share sheet. It uses one extra App ID on a free Apple account.
4. Open Fetch and pair it: scan the QR code from **Add a device** on another device with the Camera app, or paste the pairing link in Settings. On the first device, set up the hub with its address and token instead (`192.168.1.20:8787` or `fetch.example.com`; the app adds http or https itself).

With a free Apple ID, sideloaded apps count against Apple's limit of three and have to be refreshed every seven days, which SideStore does for you.

Using it:

- **Paste** sends the clipboard (text, link or picture) without the iOS permission prompt.
- **Photos** sends up to 20 pictures at once. HEIC photos are converted to JPEG so the other devices can open them.
- **Tap text** to copy it. **Tap a picture** to copy it; the arrow saves it to Photos.
- **Share sheet → Fetch** from Photos, Safari, Files or any other app.
- Swipe left to delete, right to pin.

The phone has to be able to reach the hub. A hub on your home network needs the same Wi-Fi, or a VPN such as Tailscale or WireGuard when you're out. A hub on a VPS with a domain works from anywhere.

## 5. Android

Every push also builds `Fetch.apk` and publishes it in the [releases](../../releases). It runs on Android 10 and later and asks for no permissions.

1. On the phone, download `Fetch.apk` from the latest [release](../../releases/latest) and open it. The first time, Android asks whether your browser may install apps; allow it.
2. Open Fetch and pair it: **Scan code** and point it at **Add a device** on another device, or paste the pairing link. On the first device, set up the hub with its address and token instead.

It works like the iPhone app:

- **Paste** sends the clipboard: text, a link or a copied picture.
- **Photos** sends up to 20 pictures at once. HEIC photos are converted to JPEG so the other devices can open them.
- **Tap text** or **a picture** to copy it. The arrow saves a picture to your gallery (`Pictures/Fetch`) and any other file to `Downloads`.
- **Share → Fetch** from the gallery, the browser, a file manager or any other app.
- Swipe left to delete, right to pin. A long press lists everything you can do with an item.

### Updates that install over the old version

Android only installs an update on top of the app when both were signed with the same key. Out of the box every build is signed with a throwaway key, so a newer `Fetch.apk` has to replace the app: uninstall, install, pair again.

To get updates that simply install, give your repo a key of its own, once. `keytool` comes with Java; `gh` is the GitHub command line.

```sh
keytool -genkeypair -keystore fetch.jks -alias fetch -keyalg RSA -keysize 2048 -validity 10000 \
  -storepass "choose-a-password" -dname "CN=Fetch"
base64 -w0 fetch.jks | gh secret set ANDROID_KEYSTORE_BASE64
gh secret set ANDROID_KEYSTORE_PASSWORD --body "choose-a-password"
```

Without `gh`, add the same two secrets under **Settings → Secrets and variables → Actions**. Keep `fetch.jks` somewhere safe: a build signed with a different key can't update the app either.

From the next build on, every `Fetch.apk` updates the one before it, and [Obtainium](https://github.com/ImranR98/Obtainium) can watch this repo and offer new versions on the phone.

## API

Everything is HTTP with `Authorization: Bearer <token>`, and every item is encrypted before it's sent, so items can't be posted with plain `curl` any more; use `fetch send` (above), which also works in scripts. The format, the key derivation, the pairing links and the hub's API are described in [spec/encryption.md](spec/encryption.md), with test vectors in `spec/vectors.json` that every implementation here is checked against.

| | |
|---|---|
| `GET /api/items` | the feed, newest first: id, encrypted metadata, size of the encrypted data, time, pinned |
| `POST /api/items` | an encrypted item (`application/x-fetch-sealed`) |
| `GET /api/items/{id}/raw` | the encrypted data |
| `PUT /api/items/{id}/pin?on=1` | pin or unpin (`on=0`) |
| `DELETE /api/items/{id}` | delete one |
| `DELETE /api/items` | delete everything unpinned |
| `GET`, `PUT`, `DELETE /api/vault` | the check value of the key, to tell matching devices apart; `DELETE` starts over |
| `GET /api/events` | server-sent events; a `data:` line means the feed changed |
| `GET /api/info` | version, item lifetime, upload limit, and this space's storage limit and use |
| `GET /api/hub` | no token needed: the mode, so an app knows whether it can make a space |
| `POST /api/spaces` | no token needed: `{"invite": "<code>"}` (or none on a public hub) makes a space and returns its token |
| `DELETE /api/space` | delete your own space and everything in it |
| `POST`, `GET /api/invites`, `DELETE /api/invites/{code}` | the host's own token only: make, list and revoke invite codes |
| `GET /api/spaces`, `DELETE /api/spaces/{id}` | the host's own token only: list spaces and their size, or delete one |

## Building it yourself

```sh
cd hub && go test ./... && go build -o fetch .
node --test hub/webtest/*.test.js                # the web page's encryption and code detection
```

```sh
cd desktop && npm install && npm start          # run the desktop app from source
FETCH_HUB_BIN=../hub/fetch npm test             # its unit tests
FETCH_HUB_BIN=../hub/fetch xvfb-run -a npm run test:e2e   # the real app against a real hub (Linux)
```

```sh
cd android
./gradlew testDebugUnitTest assembleDebug        # the Android app; needs a JDK (17+) and the Android SDK
python3 ../tools/codegen/kotlin-tables.py        # after changing hub/web/fetch-code.js: the Android port's tables
./gradlew assembleDebug assembleDebugAndroidTest && ci/device-test.sh   # the real app on a phone or emulator against a real hub
```

Everything is built by `.github/workflows/build.yml`: the hub on Linux, the desktop app on Windows, macOS and Linux runners, the Android app on Linux, and the iPhone app on a macOS runner (`xcodegen generate` creates the Xcode project from `project.yml`; its tests run on a simulator, and the result is packaged unsigned for SideStore). Every client's encryption is tested against `spec/vectors.json` and its code detection against `spec/code-corpus.json`. `.github/workflows/android-test.yml` runs the Android app on an emulator whenever `android/` changes. Fork the repo and the same workflow builds your own copies.
