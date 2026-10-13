package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func archiveFixture(t *testing.T, name string) []byte {
	t.Helper()
	var b bytes.Buffer
	g := gzip.NewWriter(&b)
	w := tar.NewWriter(g)
	content := []byte("binary")
	if err := w.WriteHeader(&tar.Header{Name: name, Mode: 0755, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write(content)
	_ = w.Close()
	_ = g.Close()
	return b.Bytes()
}

type releaseTransport func(*http.Request) (*http.Response, error)

func (f releaseTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestUpgradeFailurePreservesOldBinary(t *testing.T) {
	version := "v1.2.3"
	name := "mobterm-cli_" + version + "_" + platformOS + "_" + platformArch + ".tar.gz"
	base := "https://github.com/" + releaseRepo + "/releases/download/" + version + "/"
	assets := []map[string]string{}
	for _, platform := range []string{"linux_amd64", "linux_arm64", "darwin_amd64", "darwin_arm64"} {
		n := "mobterm-cli_" + version + "_" + platform + ".tar.gz"
		assets = append(assets, map[string]string{"name": n, "browser_download_url": base + n})
	}
	assets = append(assets, map[string]string{"name": "checksums.txt", "browser_download_url": base + "checksums.txt"})
	meta, _ := json.Marshal(map[string]any{"tag_name": version, "assets": assets})
	archive := archiveFixture(t, "mobterm-cli")
	validChecksum := false
	previous := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = previous })
	http.DefaultTransport = releaseTransport(func(r *http.Request) (*http.Response, error) {
		var data []byte
		if strings.Contains(r.URL.Path, "/releases/tags/") {
			data = meta
		} else if strings.HasSuffix(r.URL.Path, "checksums.txt") {
			sum := sha256.Sum256(archive)
			if !validChecksum {
				sum[0]++
			}
			data = []byte(fmt.Sprintf("%x  %s\n", sum, name))
		} else {
			data = archive
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(data)), Header: make(http.Header)}, nil
	})
	dest := filepath.Join(t.TempDir(), "mobterm-cli")
	old := []byte("previous functioning executable")
	if err := os.WriteFile(dest, old, 0755); err != nil {
		t.Fatal(err)
	}
	for _, allowChecksum := range []bool{false, true} {
		validChecksum = allowChecksum
		if err := upgrade(context.Background(), version, dest); err == nil {
			t.Fatal("invalid release replaced executable")
		}
		b, _ := os.ReadFile(dest)
		if !bytes.Equal(b, old) {
			t.Fatal("old executable changed after failure")
		}
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	if err = verifyExecutable(binary, platformOS, platformArch); err != nil {
		t.Fatal("native binary rejected", err)
	}
}
func TestReleaseVerification(t *testing.T) {
	data := archiveFixture(t, "mobterm-cli")
	sum := sha256.Sum256(data)
	line := []byte(fmt.Sprintf("%x  package.tar.gz\n", sum))
	if err := verifyChecksum(data, line, "package.tar.gz"); err != nil {
		t.Fatal(err)
	}
	if err := verifyChecksum(append(data, 1), line, "package.tar.gz"); err == nil {
		t.Fatal("corrupt archive accepted")
	}
	if _, err := extractBinary(archiveFixture(t, "../mobterm-cli")); err == nil {
		t.Fatal("unsafe archive accepted")
	}
	b, err := extractBinary(data)
	if err != nil || string(b) != "binary" {
		t.Fatal("binary extraction failed")
	}
	for _, u := range []string{"http://github.com/a", "https://evil.example/a", "https://github.com@evil.example/a"} {
		if trustedReleaseURL(u) {
			t.Fatal("untrusted origin accepted")
		}
	}
}
