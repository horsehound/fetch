// fetch is a tiny personal hub for moving text, pictures and files between
// your own devices. One binary: `fetch serve` runs the hub (with the web UI
// built in), `fetch send` and `fetch latest` are a command-line client.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

var version = "1.0.0"

const usage = `fetch - move text, pictures and files between your devices

  fetch serve [-addr :8787] [-data ./data] [-token ...] [-ttl 168h]
              [-cert cert.pem -key key.pem]
        run the hub; open the printed address in a browser

  fetch login <hub-url> <token>     remember a hub for the commands below
  fetch send <file>...              send files or pictures
  fetch send -t "some text"         send text
  <command> | fetch send            send whatever is piped in
  fetch latest                      print the newest text item
  fetch latest -raw                 write the newest item (any kind) to stdout

The client also reads FETCH_URL and FETCH_TOKEN from the environment.
`

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
	case "login":
		runLogin(args)
	case "send":
		runSend(args)
	case "latest":
		runLatest(args)
	case "version":
		fmt.Println(version)
	case "help":
		fmt.Print(usage)
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

type client struct {
	URL   string `json:"url"`
	Token string `json:"token"`
}

func clientConfigPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		die("cannot find a config directory: %v", err)
	}
	return filepath.Join(dir, "fetch", "client.json")
}

func loadClient() client {
	var c client
	if b, err := os.ReadFile(clientConfigPath()); err == nil {
		json.Unmarshal(b, &c)
	}
	if v := os.Getenv("FETCH_URL"); v != "" {
		c.URL = v
	}
	if v := os.Getenv("FETCH_TOKEN"); v != "" {
		c.Token = v
	}
	if c.URL == "" || c.Token == "" {
		die("no hub configured. Run: fetch login <hub-url> <token>")
	}
	c.URL = normalizeURL(c.URL)
	return c
}

// normalizeURL fills in the scheme people leave out: http for addresses on a
// home network (an IP, a port, a bare or .local/.lan name), https for a domain.
func normalizeURL(u string) string {
	u = strings.TrimRight(strings.TrimSpace(u), "/")
	if strings.Contains(u, "://") {
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
	req, err := http.NewRequest(method, c.URL+path, body)
	if err != nil {
		die("bad hub address %q: %v", c.URL, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if host, err := os.Hostname(); err == nil {
		req.Header.Set("X-Device", url.PathEscape(host))
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		die("cannot reach the hub at %s: %v", c.URL, err)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		die("the hub rejected the token. Run: fetch login <hub-url> <token>")
	}
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
		die("hub answered %s: %s", resp.Status, strings.TrimSpace(string(msg)))
	}
	return resp
}

func runLogin(args []string) {
	if len(args) != 2 {
		die("usage: fetch login <hub-url> <token>")
	}
	c := client{URL: normalizeURL(args[0]), Token: strings.TrimSpace(args[1])}
	c.do("GET", "/api/info", "", nil).Body.Close()
	path := clientConfigPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		die("%v", err)
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		die("%v", err)
	}
	fmt.Printf("connected to %s (saved in %s)\n", c.URL, path)
}

func runSend(args []string) {
	fs := flag.NewFlagSet("send", flag.ExitOnError)
	text := fs.String("t", "", "send this text")
	fs.Parse(args)
	c := loadClient()

	switch {
	case *text != "":
		c.do("POST", "/api/items", "text/plain; charset=utf-8", strings.NewReader(*text)).Body.Close()
	case fs.NArg() > 0:
		for _, path := range fs.Args() {
			f, err := os.Open(path)
			if err != nil {
				die("%v", err)
			}
			q := "?name=" + url.QueryEscape(filepath.Base(path))
			c.do("POST", "/api/items"+q, "application/octet-stream", f).Body.Close()
			f.Close()
		}
	default:
		if st, _ := os.Stdin.Stat(); st != nil && st.Mode()&os.ModeCharDevice != 0 {
			die("nothing to send. Give a file, -t \"text\", or pipe something in")
		}
		// No content type: the hub decides between text, image and file.
		c.do("POST", "/api/items", "application/octet-stream", os.Stdin).Body.Close()
	}
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
		if it.Kind == "text" {
			fmt.Print(it.Text)
			return
		}
		if *raw {
			body := c.do("GET", "/api/items/"+it.ID+"/raw", "", nil).Body
			defer body.Close()
			if _, err := io.Copy(os.Stdout, body); err != nil {
				die("download interrupted: %v", err)
			}
			return
		}
	}
	die("nothing there yet")
}
