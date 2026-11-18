// fetch is a tiny personal hub for moving text, pictures and files between
// your own devices, end-to-end encrypted. One binary: `fetch serve` runs the
// hub (with the web page built in); the other commands are a command-line
// client that encrypts and decrypts on your machine.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

var version = "1.0.0"

const usage = `fetch - move text, pictures and files between your devices

  fetch serve [-addr :8787] [-data ./data] [-token ...] [-ttl 168h]
              [-cert cert.pem -key key.pem]
        run the hub; open the printed address in a browser
  fetch update [-check]             install the newest release and restart the hub
  fetch version                     print this version

  fetch pair '<link>'               join with a pairing link from another device
  fetch init <hub-url> <token>      set up a new hub from here (creates the key)
  fetch link                        print the pairing link for another device
  fetch send <file>...              send files or pictures
  fetch send -t "some text"         send text
  <command> | fetch send            send whatever is piped in
  fetch latest                      print the newest text item
  fetch latest -raw                 write the newest item (any kind) to stdout

Everything is encrypted here before it is sent; the hub never sees the key.
FETCH_LINK, if set, is used instead of the saved pairing.
`

// Text up to this size travels inside the encrypted metadata; longer text is
// sent as a .txt file. Every client uses the same limit.
const maxTextInMeta = 256 << 10

func main() {
	args := os.Args[1:]
	cmd := "serve"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	} else if len(args) > 0 && (args[0] == "-h" || args[0] == "--help" || args[0] == "-help") {
		cmd = "help"
	}
	switch cmd {
	case "serve":
		runServe(args)
	case "pair":
		runPair(args)
	case "init":
		runInit(args)
	case "link":
		fmt.Println(loadClient().Link())
	case "send":
		runSend(args)
	case "latest":
		runLatest(args)
	case "update":
		runUpdate(args)
	case "version":
		fmt.Println(version)
	case "help":
		fmt.Print(usage)
	case "login":
		die("fetch now encrypts everything. Pair this machine instead: on a device that is set up, open \"Add a device\", copy the link, then run: fetch pair '<link>'")
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}
}

func die(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "fetch: "+format+"\n", a...)
	os.Exit(1)
}

// ---- client ----

type client struct{ Pairing }

type savedClient struct {
	URL   string `json:"url"`
	Token string `json:"token"`
	Key   string `json:"key"`
}

func clientConfigPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		die("cannot find a config directory: %v", err)
	}
	return filepath.Join(dir, "fetch", "client.json")
}

func loadClient() client {
	if link := os.Getenv("FETCH_LINK"); link != "" {
		p, err := ParsePairing(link)
		if err != nil {
			die("FETCH_LINK: %v", err)
		}
		return client{p}
	}
	var saved savedClient
	b, err := os.ReadFile(clientConfigPath())
	if err == nil {
		err = json.Unmarshal(b, &saved)
	}
	key, kerr := ParseKey(saved.Key)
	if err != nil || kerr != nil || saved.URL == "" || saved.Token == "" {
		die("this machine isn't paired. Run: fetch pair '<link>'  (the link is under \"Add a device\" on a device that is set up)")
	}
	return client{Pairing{Hub: normalizeURL(saved.URL), Token: saved.Token, Key: key}}
}

func (c client) save() {
	path := clientConfigPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		die("%v", err)
	}
	b, _ := json.MarshalIndent(savedClient{URL: c.Hub, Token: c.Token, Key: c.Key.String()}, "", "  ")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		die("%v", err)
	}
	os.Chmod(path, 0o600)
}

// normalizeURL fills in the scheme people leave out: http for addresses on a
// home network (an IP, a port, a bare or .local/.lan name), https for a domain.
func normalizeURL(u string) string {
	u = strings.TrimRight(strings.TrimSpace(u), "/")
	if u == "" || strings.Contains(u, "://") {
		return u
	}
	host, _, _ := strings.Cut(u, "/")
	local := strings.Contains(host, ":") || !strings.Contains(host, ".") ||
		strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".lan") ||
		strings.Trim(host, "0123456789.") == ""
	if local {
		return "http://" + u
	}
	return "https://" + u
}

func (c client) do(method, path, contentType string, body io.Reader) *http.Response {
	req, err := http.NewRequest(method, c.Hub+path, body)
	if err != nil {
		die("bad hub address %q: %v", c.Hub, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		die("cannot reach the hub at %s: %v", c.Hub, err)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		die("the hub rejected the token; pair again with a fresh link")
	}
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
		die("hub answered %s: %s", resp.Status, strings.TrimSpace(string(msg)))
	}
	return resp
}

func (c client) hubCheck() string {
	resp := c.do("GET", "/api/vault", "", nil)
	defer resp.Body.Close()
	var v struct {
		Check string `json:"check"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		die("%s answered, but it isn't a Fetch hub with encryption (update the hub?)", c.Hub)
	}
	return v.Check
}

func (c client) claimHub() {
	body, _ := json.Marshal(map[string]string{"check": c.Key.Check()})
	c.do("PUT", "/api/vault", "application/json", bytes.NewReader(body)).Body.Close()
}

func runPair(args []string) {
	if len(args) != 1 {
		die("usage: fetch pair '<link>'   (quote it: the link contains & characters)")
	}
	p, err := ParsePairing(args[0])
	if err != nil {
		die("%v", err)
	}
	c := client{p}
	switch check := c.hubCheck(); check {
	case "":
		c.claimHub() // the hub was reset since the link was made
	case c.Key.Check():
	default:
		die("this link's key isn't the one the hub uses now. Make a fresh link on a device that works")
	}
	c.save()
	fmt.Printf("paired with %s (key %s)\n", c.Hub, fingerprint(c.Key))
}

func runInit(args []string) {
	if len(args) != 2 {
		die("usage: fetch init <hub-url> <token>")
	}
	key, err := NewKey()
	if err != nil {
		die("%v", err)
	}
	c := client{Pairing{Hub: normalizeURL(args[0]), Token: strings.TrimSpace(args[1]), Key: key}}
	if c.hubCheck() != "" {
		die("this hub already has a key. Pair from a device that has it: open \"Add a device\" there and run fetch pair '<link>'")
	}
	c.claimHub()
	c.save()
	fmt.Printf("created a new key for %s (key %s)\n\nAdd your other devices with this pairing link. It unlocks everything: keep it to yourself.\n\n%s\n",
		c.Hub, fingerprint(c.Key), c.Link())
}

// fingerprint is a short form of the key's check, to compare devices by eye.
func fingerprint(k Key) string {
	c := strings.ToUpper(k.Check()[:8])
	return c[:4] + "-" + c[4:]
}

func hostname() string {
	if h, err := os.Hostname(); err == nil {
		return h
	}
	return "Terminal"
}

// upload encrypts one item here and sends it.
func (c client) upload(m Meta, data io.Reader) {
	m.Device = hostname()
	pr, pw := io.Pipe()
	go func() { pw.CloseWithError(c.Key.WriteUpload(pw, m, data)) }()
	c.do("POST", "/api/items", sealedType, pr).Body.Close()
}

func runSend(args []string) {
	fs := flag.NewFlagSet("send", flag.ExitOnError)
	text := fs.String("t", "", "send this text")
	fs.Parse(args)
	c := loadClient()

	switch {
	case *text != "":
		c.sendText(*text)
	case fs.NArg() > 0:
		for _, path := range fs.Args() {
			f, err := os.Open(path)
			if err != nil {
				die("%v", err)
			}
			st, err := f.Stat()
			if err != nil || st.IsDir() {
				die("%s isn't a file", path)
			}
			head := make([]byte, 512)
			n, _ := io.ReadFull(f, head)
			f.Seek(0, io.SeekStart)
			c.sendFile(f, filepath.Base(path), head[:n], st.Size())
			f.Close()
		}
	default:
		if st, _ := os.Stdin.Stat(); st != nil && st.Mode()&os.ModeCharDevice != 0 {
			die("nothing to send. Give a file, -t \"text\", or pipe something in")
		}
		// Short text goes as text; anything else (a screenshot, an archive) as a file.
		head, err := io.ReadAll(io.LimitReader(os.Stdin, maxTextInMeta+1))
		if err != nil {
			die("%v", err)
		}
		if len(head) <= maxTextInMeta && utf8.Valid(head) && !bytes.ContainsRune(head, 0) {
			c.sendText(strings.TrimSuffix(string(head), "\n")) // piped output ends in a newline nobody meant
			return
		}
		c.sendFile(io.MultiReader(bytes.NewReader(head), os.Stdin), "", head, 0)
	}
}

func (c client) sendText(text string) {
	if strings.TrimSpace(text) == "" {
		die("nothing to send")
	}
	if len(text) > maxTextInMeta {
		name := "text-" + time.Now().Format("20060102-150405") + ".txt"
		c.upload(Meta{Kind: "file", Name: name, Mime: "text/plain", Size: int64(len(text))}, strings.NewReader(text))
		return
	}
	c.upload(Meta{Kind: "text", Text: text}, nil)
}

func (c client) sendFile(r io.Reader, name string, head []byte, size int64) {
	mt := mimeFor(name, head)
	kind := "file"
	if inlineImage[mt] {
		kind = "image"
	}
	if name == "" {
		name = kind + "-" + time.Now().Format("20060102-150405") + extFor(mt)
	}
	c.upload(Meta{Kind: kind, Name: name, Mime: mt, Size: size}, r)
}

func runLatest(args []string) {
	fs := flag.NewFlagSet("latest", flag.ExitOnError)
	raw := fs.Bool("raw", false, "write the newest item of any kind to stdout")
	fs.Parse(args)
	c := loadClient()

	resp := c.do("GET", "/api/items", "", nil)
	var list struct {
		Items []Item `json:"items"`
	}
	err := json.NewDecoder(resp.Body).Decode(&list)
	resp.Body.Close()
	if err != nil {
		die("unexpected answer from the hub: %v", err)
	}
	for _, it := range list.Items {
		m, salt, err := c.Key.OpenMeta(it.Meta)
		if err != nil {
			continue // made with another key; nothing to show
		}
		if m.Kind == "text" {
			fmt.Print(m.Text)
			return
		}
		if *raw {
			body := c.do("GET", "/api/items/"+it.ID+"/raw", "", nil).Body
			defer body.Close()
			if _, err := io.Copy(os.Stdout, c.Key.OpenData(body, salt)); err != nil {
				die("%v", err)
			}
			return
		}
	}
	die("no text in the feed yet")
}

// ---- file types ----

// Types every client can show inline. SVG is deliberately not one of them: it
// can carry script, so it always travels as a file.
var inlineImage = map[string]bool{
	"image/png": true, "image/jpeg": true, "image/gif": true,
	"image/webp": true, "image/avif": true, "image/bmp": true,
}

var commonExt = map[string]string{
	"image/png": ".png", "image/jpeg": ".jpg", "image/gif": ".gif",
	"image/webp": ".webp", "image/avif": ".avif", "image/bmp": ".bmp",
	"application/pdf": ".pdf", "application/zip": ".zip", "text/plain": ".txt",
}

func extFor(mt string) string {
	if e, ok := commonExt[mt]; ok {
		return e
	}
	if exts, _ := mime.ExtensionsByType(mt); len(exts) > 0 {
		return exts[0]
	}
	return ""
}

func mimeFor(name string, head []byte) string {
	if byExt, _, err := mime.ParseMediaType(mime.TypeByExtension(strings.ToLower(filepath.Ext(name)))); err == nil && byExt != "" {
		return byExt
	}
	if sniffed, _, err := mime.ParseMediaType(http.DetectContentType(head)); err == nil {
		return sniffed
	}
	return "application/octet-stream"
}
