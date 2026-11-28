package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func archiveFixture(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var b bytes.Buffer
	g, err := gzip.NewWriterLevel(&b, gzip.BestSpeed)
	if err != nil {
		t.Fatal(err)
	}
	w := tar.NewWriter(g)
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
func releaseFixture(t *testing.T, filename string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", filename))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func releaseResponse(status int, data []byte) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(bytes.NewReader(data)), Header: make(http.Header)}
}

// Serve the repository lookup, canonical Release API, ID-based asset downloads and
// CDN redirect as separate routes. Unknown requests fail the test before any I/O.
type releaseRoute struct {
	body     []byte
	redirect string
	status   int
}

func (routes releaseRoutes) transport(t *testing.T) releaseTransport {
	t.Helper()
	return func(r *http.Request) (*http.Response, error) {
		route, ok := routes[r.URL.String()]
		if !ok {
			t.Fatalf("unexpected endpoint %s", r.URL)
		}
		media := githubMetadata
		if strings.Contains(r.URL.Path, "/releases/assets/") || r.URL.Host != "api.github.com" {
			media = githubAsset
		}
		if r.Header.Get("Accept") != media {
			t.Fatalf("unexpected media type at %s", r.URL.Path)
		}
		status := route.status
		if status == 0 {
			status = http.StatusOK
		}
		response := releaseResponse(status, route.body)
		if route.redirect != "" {
			response.StatusCode = http.StatusFound
			response.Header.Set("Location", route.redirect)
		}
		return response, nil
	}
}

type releaseRoutes map[string]releaseRoute

func TestUpgradeTransferredRepository(t *testing.T) {
	const version = "v1.2.3"
	const canonical = repositoryAPI + "example-org/renamed-cli"
	const repoLookup = "https://api.github.com/repositories/1416491919"
	const releaseURL = canonical + "/releases/tags/" + version
	const assetBase = canonical + "/releases/assets/"
	const cdn = "https://release-assets.githubusercontent.com:443/fixture"

	repo := releaseFixture(t, "release-repository.json")
	manifest := releaseFixture(t, "release-stable.json")
	platformAsset := "mobterm-cli_" + version + "_" + platformOS + "_" + platformArch + ".tar.gz"
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	native, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	archive := archiveFixture(t, "mobterm-cli", native)
	sum := fmt.Sprintf("%x", sha256.Sum256(archive))
	checks := sum + "  " + platformAsset + "\n"

	var release map[string]any
	if err := json.Unmarshal([]byte(manifest), &release); err != nil {
		t.Fatal(err)
	}
	var archiveURL string
	for _, item := range release["assets"].([]any) {
		asset := item.(map[string]any)
		switch asset["name"] {
		case platformAsset:
			archiveURL = asset["url"].(string)
			asset["digest"] = "sha256:" + sum
		case "checksums.txt":
			asset["digest"] = fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(checks)))
		}
	}
	withDigest, err := json.Marshal(release)
	if err != nil || archiveURL == "" {
		t.Fatal("invalid release fixture", err)
	}
	wrongRepo := strings.ReplaceAll(repo, "1416491919", "1416491920")
	foreign := "https://api.github.com/repos/example-org/other-cli/releases/"
	invalidArchive := archiveFixture(t, "mobterm-cli", []byte("wrong format"))
	replace := func(original, old, replacement string) string {
		t.Helper()
		if !strings.Contains(original, old) {
			t.Fatalf("fixture lacks %q", old)
		}
		return strings.Replace(original, old, replacement, 1)
	}
	old := []byte("previous functioning executable")
	previous := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = previous })
	for _, test := range []struct {
		name, route, body, failure string
		status                     int
	}{
		{name: "asset-digests", route: releaseURL, body: string(withDigest)},
		{name: "legacy-checksums"},
		{name: "different-repo", route: repoLookup, body: wrongRepo, failure: "unexpected release source"},
		{name: "changed-owner-during-lookup", route: canonical, body: wrongRepo, failure: "unexpected release source"},
		{name: "duplicate-repo-id", route: repoLookup, body: `{"id":1416491919,"id":10,"full_name":"example-org/renamed-cli"}`, failure: "invalid GitHub release metadata"},
		{name: "missing-root-id", route: repoLookup, body: `{"owner":{"id":1416491919},"full_name":"example-org/renamed-cli"}`, failure: "invalid GitHub release metadata"},
		{name: "wrong-tag", route: releaseURL, body: replace(manifest, `"v1.2.3"`, `"v1.2.4"`), failure: "requested stable release unavailable"},
		{name: "prerelease", route: releaseURL, body: replace(manifest, `"prerelease": false`, `"prerelease": true`), failure: "requested stable release unavailable"},
		{name: "missing-draft", route: releaseURL, body: replace(manifest, `"draft": false,`, ""), failure: "invalid GitHub release metadata"},
		{name: "string-bool", route: releaseURL, body: replace(manifest, `"draft": false`, `"draft": "false"`), failure: "invalid GitHub release metadata"},
		{name: "missing-release-id", route: releaseURL, body: replace(manifest, `"id": 74,`, ""), failure: "invalid GitHub release metadata"},
		{name: "duplicate-escaped-key", route: releaseURL, body: replace(manifest, `"draft": false`, `"dr\u0061ft": true,"draft":false`), failure: "invalid GitHub release metadata"},
		{name: "wrong-release-url", route: releaseURL, body: replace(manifest, canonical+"/releases/74", foreign+"74"), failure: "unexpected release source"},
		{name: "wrong-asset-url", route: releaseURL, body: replace(manifest, assetBase+"105", foreign+"assets/105"), failure: "unexpected release source"},
		{name: "duplicate-asset-name", route: releaseURL, body: replace(manifest, `"name": "checksums.txt"`, `"name": "`+platformAsset+`"`), failure: "duplicate release asset"},
		{name: "incomplete-platforms", route: releaseURL, body: replace(manifest, platformAsset, "another-package"), failure: "release is incomplete"},
		{name: "oversize", route: releaseURL, body: manifest + strings.Repeat(" ", maxReleaseMetadata), failure: "exceeds limit"},
		{name: "truncated", route: releaseURL, body: "{", failure: "invalid GitHub release metadata"},
		{name: "missing-release", route: releaseURL, status: 404, failure: "requested release unavailable"},
		{name: "repo-unavailable", route: repoLookup, status: 404, failure: "official repository unavailable"},
		{name: "rate-limit", route: repoLookup, status: 429, failure: "rate limit"},
		{name: "invalid-digest", route: releaseURL, body: replace(string(withDigest), "sha256:", "sha1:"), failure: "invalid release asset digest"},
		{name: "bad-archive-digest", route: releaseURL, body: replace(string(withDigest), sum, strings.Repeat("0", 64)), failure: "digest mismatch"},
		{name: "bad-checksum-digest", route: releaseURL, body: replace(string(withDigest), fmt.Sprintf("%x", sha256.Sum256([]byte(checks))), strings.Repeat("0", 64)), failure: "digest mismatch"},
		{name: "bad-checksum", route: assetBase + "105", body: replace(checks, sum, strings.Repeat("0", 64)), failure: "release checksum mismatch"},
		{name: "duplicate-checksum", route: assetBase + "105", body: checks + checks, failure: "ambiguous"},
		{name: "wrong-platform", route: cdn, body: string(invalidArchive), failure: "invalid"},
	} {
		t.Run(test.name, func(t *testing.T) {
			dest := filepath.Join(t.TempDir(), "mobterm-cli")
			if err := os.WriteFile(dest, old, 0755); err != nil {
				t.Fatal(err)
			}
			routes := releaseRoutes{
				repositoryAPI + releaseRepo: {redirect: repoLookup},
				repoLookup:                  {body: []byte(repo)},
				canonical:                   {body: []byte(repo)},
				releaseURL:                  {body: []byte(manifest)},
				assetBase + "105":           {body: []byte(checks)},
				archiveURL:                  {redirect: cdn},
				cdn:                         {body: archive},
			}
			if test.route != "" {
				routes[test.route] = releaseRoute{body: []byte(test.body), status: test.status}
			}
			if test.name == "wrong-platform" {
				routes[assetBase+"105"] = releaseRoute{body: []byte(fmt.Sprintf("%x  %s\n", sha256.Sum256(invalidArchive), platformAsset))}
			}
			http.DefaultTransport = routes.transport(t)
			err := upgrade(context.Background(), version, dest)
			want := old
			if test.failure == "" {
				if err != nil {
					t.Fatal(err)
				}
				want = native
			} else if err == nil || !strings.Contains(err.Error(), test.failure) {
				t.Fatalf("wanted %q, got %v", test.failure, err)
			}
			got, err := os.ReadFile(dest)
			if err != nil || !bytes.Equal(got, want) {
				t.Fatalf("unexpected binary after upgrade: %v", err)
			}
		})
	}
}

func TestUpgradeRedirectBoundary(t *testing.T) {
	for _, test := range []struct{ name, media, target string }{
		{"metadata-CDN", githubMetadata, "https://objects.githubusercontent.com/path"},
		{"asset-http", githubAsset, "http://github.com/path"},
		{"asset-foreign", githubAsset, "https://other.example/path"},
		{"asset-userinfo", githubAsset, "https://example@github.com/path"},
		{"asset-port", githubAsset, "https://github.com:444/path"},
		{"asset-empty-port", githubAsset, "https://github.com:/path"},
		{"asset-fragment", githubAsset, "https://github.com/path#fragment"},
		{"metadata-loop", githubMetadata, "https://api.github.com/loop"},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			transport := releaseTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				response := releaseResponse(302, nil)
				response.Header.Set("Location", test.target)
				return response, nil
			})
			_, err := download(context.Background(), transport, "https://api.github.com/start", test.media, 256, nil)
			wantCalls := 1
			if test.name == "metadata-loop" {
				wantCalls = 6
			}
			if !errors.Is(err, errReleaseSource) || calls != wantCalls {
				t.Fatalf("requests %d, err %v", calls, err)
			}
		})
	}
}

func TestReleaseVerification(t *testing.T) {
	data := archiveFixture(t, "mobterm-cli", []byte("binary"))
	sum := sha256.Sum256(data)
	line := []byte(fmt.Sprintf("%x  package.tar.gz\n", sum))
	if err := verifyChecksum(data, line, "package.tar.gz"); err != nil {
		t.Fatal(err)
	}
	if err := verifyChecksum(data, append(line, 0), "package.tar.gz"); err == nil {
		t.Fatal("invalid checksums file accepted")
	}
	if err := verifyChecksum(append(data, 1), line, "package.tar.gz"); err == nil {
		t.Fatal("corrupt archive accepted")
	}
	if _, err := extractBinary(archiveFixture(t, "../mobterm-cli", []byte("binary"))); err == nil {
		t.Fatal("unsafe archive accepted")
	}
	b, err := extractBinary(data)
	if err != nil || string(b) != "binary" {
		t.Fatal("binary extraction failed")
	}
}
