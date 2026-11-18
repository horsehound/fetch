package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// The repository whose releases `fetch update` installs. The release build
// sets it to the repository that built it, so a fork updates from itself.
var updateRepo = "horsehound/fetch"

// The systemd unit from hub/fetch-hub.service, restarted after an update.
const serviceName = "fetch-hub"

type release struct {
	Tag    string `json:"tag_name"`
	Assets []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

func (r release) asset(name string) string {
	for _, a := range r.Assets {
		if a.Name == name {
			return a.URL
		}
	}
	return ""
}

var updateHTTP = &http.Client{Timeout: 10 * time.Minute}

// runUpdate replaces this binary with the newest release: checked against the
// release's SHA256SUMS, tried once, swapped in atomically, and the hub
// service restarted if it runs under systemd.
func runUpdate(args []string) {
	fs := flag.NewFlagSet("update", flag.ExitOnError)
	check := fs.Bool("check", false, "only say whether there is a newer version")
	force := fs.Bool("force", false, "install the latest release even if it isn't newer")
	quiet := fs.Bool("quiet", false, "say nothing when already up to date (for timers)")
	fs.Parse(args)

	if inContainer() {
		die("this hub runs in a container. Update it from the machine running Docker:\n  docker compose pull && docker compose up -d")
	}

	rel, err := latestRelease()
	if err != nil {
		die("cannot look up the latest release: %v", err)
	}
	latest := strings.TrimPrefix(rel.Tag, "v")
	newer := newerVersion(latest, version)
	switch {
	case !newer && !*force:
		if !*quiet {
			fmt.Printf("fetch %s is up to date (latest release: %s)\n", version, latest)
		}
		return
	case *check:
		fmt.Printf("fetch %s is available (this is %s). Run: fetch update\n", latest, version)
		return
	}

	name := assetName(runtime.GOOS, runtime.GOARCH)
	binURL, sumsURL := rel.asset(name), rel.asset("SHA256SUMS")
	if binURL == "" {
		die("release %s has no build for this system (%s)", rel.Tag, name)
	}
	if sumsURL == "" {
		die("release %s has no SHA256SUMS to check the download against; update by hand this once", rel.Tag)
	}
	want, err := checksumFor(sumsURL, name)
	if err != nil {
		die("cannot read the checksums of %s: %v", rel.Tag, err)
	}

	self, err := os.Executable()
	if err == nil {
		self, err = filepath.EvalSymlinks(self)
	}
	if err != nil {
		die("cannot find this program's file: %v", err)
	}

	fmt.Printf("updating fetch %s → %s\n", version, latest)
	tmp, err := download(binURL, filepath.Dir(self), want)
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			die("no permission to replace %s; run it as root (sudo fetch update)", self)
		}
		die("download failed: %v", err)
	}
	defer os.Remove(tmp)

	// The new binary has to run here and be the version it claims to be.
	out, err := exec.Command(tmp, "version").Output()
	if got := strings.TrimSpace(string(out)); err != nil || got != latest {
		die("the downloaded binary doesn't run correctly on this machine (said %q, %v); nothing was changed", got, err)
	}
	if err := replace(self, tmp); err != nil {
		die("cannot replace %s: %v", self, err)
	}
	fmt.Printf("installed %s\n", self)
	restartService()
}

func latestRelease() (release, error) {
	var rel release
	api := os.Getenv("FETCH_UPDATE_API") // for tests
	if api == "" {
		api = "https://api.github.com/repos/" + updateRepo
	}
	req, err := http.NewRequest("GET", api+"/releases/latest", nil)
	if err != nil {
		return rel, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := updateHTTP.Do(req)
	if err != nil {
		return rel, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return rel, fmt.Errorf("%s answered %s", api, resp.Status)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&rel); err != nil {
		return rel, err
	}
	if rel.Tag == "" {
		return rel, errors.New("the release has no version")
	}
	return rel, nil
}

func assetName(goos, goarch string) string {
	name := "fetch-" + goos + "-" + goarch
	if goos == "windows" {
		name += ".exe"
	}
	return name
}

// newerVersion reports whether version a is newer than b. Both are dotted
// numbers ("1.0.57"); a development build ("dev") is never considered newer
// or older than a release, so it is only replaced with -force.
func newerVersion(a, b string) bool {
	pa, oka := parseVersion(a)
	pb, okb := parseVersion(b)
	if !oka || !okb {
		return false
	}
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x = pa[i]
		}
		if i < len(pb) {
			y = pb[i]
		}
		if x != y {
			return x > y
		}
	}
	return false
}

func parseVersion(s string) ([]int, bool) {
	parts := strings.Split(strings.TrimPrefix(s, "v"), ".")
	out := make([]int, len(parts))
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return nil, false
		}
		out[i] = n
	}
	return out, true
}

func checksumFor(url, name string) (string, error) {
	resp, err := updateHTTP.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s", resp.Status)
	}
	sc := bufio.NewScanner(io.LimitReader(resp.Body, 1<<20))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name && len(f[0]) == 64 {
			return strings.ToLower(f[0]), nil
		}
	}
	return "", fmt.Errorf("%s isn't listed", name)
}

// download saves url into a new executable file in dir, and returns its path
// only if its SHA-256 is want.
func download(url, dir, want string) (string, error) {
	f, err := os.CreateTemp(dir, ".fetch-update-*")
	if err != nil {
		return "", err
	}
	path := f.Name()
	fail := func(err error) (string, error) {
		f.Close()
		os.Remove(path)
		return "", err
	}
	resp, err := updateHTTP.Get(url)
	if err != nil {
		return fail(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fail(fmt.Errorf("%s", resp.Status))
	}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, 200<<20)); err != nil {
		return fail(err)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		return fail(fmt.Errorf("checksum mismatch: got %s, the release says %s", got, want))
	}
	if err := f.Chmod(0o755); err != nil {
		return fail(err)
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		return "", err
	}
	return path, nil
}

// replace swaps newPath in for self. On Linux and macOS a rename over the
// running binary is atomic and safe; Windows won't overwrite a running
// program, but lets it be renamed out of the way first.
func replace(self, newPath string) error {
	if runtime.GOOS == "windows" {
		old := self + ".old"
		os.Remove(old)
		if err := os.Rename(self, old); err != nil {
			return err
		}
		if err := os.Rename(newPath, self); err != nil {
			os.Rename(old, self)
			return err
		}
		return nil
	}
	return os.Rename(newPath, self)
}

// restartService restarts the hub if it runs as the systemd service, so the
// new version is live right away. Anywhere else it says what to do.
func restartService() {
	if runtime.GOOS == "linux" {
		if _, err := exec.LookPath("systemctl"); err == nil {
			if exec.Command("systemctl", "is-active", "--quiet", serviceName).Run() == nil {
				if out, err := exec.Command("systemctl", "restart", serviceName).CombinedOutput(); err != nil {
					fmt.Printf("restart it yourself: systemctl restart %s (%s)\n", serviceName, strings.TrimSpace(string(out)))
					return
				}
				fmt.Printf("restarted %s\n", serviceName)
				return
			}
		}
	}
	fmt.Println("restart the hub to run the new version")
}

// inContainer is true inside Docker or Podman, whose images are replaced
// rather than updated in place. (Not in an LXC container: that is a whole
// system, where updating in place is right.)
func inContainer() bool {
	for _, f := range []string{"/.dockerenv", "/run/.containerenv"} {
		if _, err := os.Stat(f); err == nil {
			return true
		}
	}
	return false
}
