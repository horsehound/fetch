package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestNewerVersion(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"1.0.58", "1.0.57", true},
		{"1.0.57", "1.0.57", false},
		{"1.0.9", "1.0.10", false},
		{"1.1", "1.0.99", true},
		{"v1.0.10", "1.0.9", true},
		{"1.0.10", "dev", false},
		{"nonsense", "1.0.0", false},
	}
	for _, c := range cases {
		if got := newerVersion(c.a, c.b); got != c.want {
			t.Errorf("newerVersion(%q, %q) = %v", c.a, c.b, got)
		}
	}
}

func TestAssetNames(t *testing.T) {
	if got := assetName("linux", "arm64"); got != "fetch-linux-arm64" {
		t.Error(got)
	}
	if got := assetName("windows", "amd64"); got != "fetch-windows-amd64.exe" {
		t.Error(got)
	}
}

// A fake release server: the release, its checksums and the binary.
func fakeReleases(t *testing.T, binary []byte, sums string) *httptest.Server {
	t.Helper()
	name := assetName(runtime.GOOS, runtime.GOARCH)
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/releases/latest":
			fmt.Fprintf(w, `{"tag_name":"v9.9.9","assets":[{"name":%q,"browser_download_url":"%s/bin"},{"name":"SHA256SUMS","browser_download_url":"%s/sums"}]}`, name, srv.URL, srv.URL)
		case "/sums":
			w.Write([]byte(sums))
		case "/bin":
			w.Write(binary)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FETCH_UPDATE_API", srv.URL)
	return srv
}

func TestDownloadIsChecked(t *testing.T) {
	binary := []byte("#!/bin/sh\necho 9.9.9\n")
	sum := sha256.Sum256(binary)
	name := assetName(runtime.GOOS, runtime.GOARCH)
	fakeReleases(t, binary, hex.EncodeToString(sum[:])+"  "+name+"\n0000000000000000000000000000000000000000000000000000000000000000  other\n")

	rel, err := latestRelease()
	if err != nil || rel.Tag != "v9.9.9" {
		t.Fatalf("release %+v, %v", rel, err)
	}
	want, err := checksumFor(rel.asset("SHA256SUMS"), name)
	if err != nil || want != hex.EncodeToString(sum[:]) {
		t.Fatalf("checksum %q, %v", want, err)
	}
	dir := t.TempDir()
	path, err := download(rel.asset(name), dir, want)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != string(binary) {
		t.Fatal("downloaded bytes differ")
	}

	// A binary that doesn't match its checksum is thrown away.
	if _, err := download(rel.asset(name), dir, "00"+want[2:]); err == nil {
		t.Fatal("a wrong checksum was accepted")
	}
	left, _ := filepath.Glob(filepath.Join(dir, ".fetch-update-*"))
	if len(left) != 1 { // only the good one from above
		t.Fatalf("leftover files: %v", left)
	}
	if _, err := checksumFor(rel.asset("SHA256SUMS"), "fetch-plan9-mips"); err == nil {
		t.Fatal("found a checksum for a build that isn't listed")
	}
}

func TestReplaceSwapsTheFile(t *testing.T) {
	dir := t.TempDir()
	self := filepath.Join(dir, "fetch")
	next := filepath.Join(dir, "next")
	os.WriteFile(self, []byte("old"), 0o755)
	os.WriteFile(next, []byte("new"), 0o755)
	if err := replace(self, next); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(self); string(b) != "new" {
		t.Fatalf("got %q", b)
	}
}
