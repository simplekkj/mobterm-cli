package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"debug/elf"
	"debug/macho"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"runtime"
	"strings"
	"time"
)

var releaseVersion = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

const releaseRepo = "mobterm/mobterm-cli"

func trustedReleaseURL(raw string) bool {
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "https" || u.User != nil {
		return false
	}
	return u.Host == "github.com" || u.Host == "api.github.com" || u.Host == "release-assets.githubusercontent.com" || u.Host == "objects.githubusercontent.com"
}
func download(ctx context.Context, c *http.Client, raw string, limit int64, progress *upgradeProgress) ([]byte, error) {
	if !trustedReleaseURL(raw) {
		return nil, errors.New("untrusted download source")
	}
	r, e := http.NewRequestWithContext(ctx, "GET", raw, nil)
	if e != nil {
		return nil, e
	}
	resp, e := c.Do(r)
	if e != nil {
		if progress != nil && ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("release download failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("release download HTTP %d", resp.StatusCode)
	}
	body := io.LimitReader(resp.Body, limit+1)
	if progress != nil && progress.mode != "never" {
		progress.startBody(resp.ContentLength)
		body = io.TeeReader(body, progress)
	}
	b, e := io.ReadAll(body)
	if e != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("release download interrupted")
	}
	if int64(len(b)) > limit {
		return nil, errors.New("release asset exceeds limit")
	}
	return b, nil
}
func extractBinary(archive []byte) ([]byte, error) {
	g, e := gzip.NewReader(bytes.NewReader(archive))
	if e != nil {
		return nil, e
	}
	defer g.Close()
	t := tar.NewReader(io.LimitReader(g, 128*1024*1024))
	var binary []byte
	for {
		h, e := t.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, e
		}
		if h.Typeflag != tar.TypeReg || h.Name == "" || strings.Contains(h.Name, "/") || h.Size < 0 || h.Size > 64*1024*1024 {
			return nil, errors.New("unsafe release archive")
		}
		switch h.Name {
		case "mobterm-cli":
			if binary != nil {
				return nil, errors.New("duplicate binary")
			}
			binary, e = io.ReadAll(t)
			if e != nil {
				return nil, e
			}
		case "LICENSE", "README.md", "THIRD_PARTY.md", "build-info.json":
		default:
			return nil, errors.New("unexpected release entry")
		}
	}
	if len(binary) == 0 {
		return nil, errors.New("release lacks executable")
	}
	return binary, nil
}
func verifyChecksum(data, checks []byte, name string) error {
	sum := sha256.Sum256(data)
	want := hex.EncodeToString(sum[:])
	count := 0
	for _, line := range strings.Split(string(checks), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == name {
			count++
			if fields[0] != want {
				return errors.New("release checksum mismatch")
			}
		}
	}
	if count != 1 {
		return errors.New("release checksum missing or ambiguous")
	}
	return nil
}

func verifyExecutable(data []byte, system, arch string) error {
	if system == "linux" {
		f, err := elf.NewFile(bytes.NewReader(data))
		if err != nil {
			return errors.New("invalid Linux executable")
		}
		defer f.Close()
		want := elf.EM_X86_64
		if arch == "arm64" {
			want = elf.EM_AARCH64
		}
		if f.Class != elf.ELFCLASS64 || f.Machine != want || (f.Type != elf.ET_EXEC && f.Type != elf.ET_DYN) {
			return errors.New("release executable platform mismatch")
		}
		return nil
	}
	if system == "darwin" {
		f, err := macho.NewFile(bytes.NewReader(data))
		if err != nil {
			return errors.New("invalid macOS executable")
		}
		defer f.Close()
		want := macho.CpuAmd64
		if arch == "arm64" {
			want = macho.CpuArm64
		}
		if f.Cpu != want || f.Type != macho.TypeExec {
			return errors.New("release executable platform mismatch")
		}
		return nil
	}
	return errors.New("unsupported executable platform")
}
func upgrade(ctx context.Context, version, destination string) error {
	return upgradeWithProgress(ctx, version, destination, nil)
}

func upgradeWithProgress(ctx context.Context, version, destination string, progress *upgradeProgress) error {
	defer progress.finish()
	if !releaseVersion.MatchString(version) {
		return errors.New("upgrade requires explicit --version vX.Y.Z")
	}
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		return errors.New("unsupported platform")
	}
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		return errors.New("unsupported architecture")
	}
	c := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) > 5 || !trustedReleaseURL(r.URL.String()) {
			return errors.New("untrusted release redirect")
		}
		return nil
	}}
	progress.begin("Fetching release metadata", "Release")
	meta, e := download(ctx, c, "https://api.github.com/repos/"+releaseRepo+"/releases/tags/"+version, 1024*1024, progress)
	if e != nil {
		return e
	}
	var r struct {
		TagName    string `json:"tag_name"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
		Assets     []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if json.Unmarshal(meta, &r) != nil || r.TagName != version || r.Draft || r.Prerelease {
		return errors.New("invalid stable release")
	}
	prefix := "https://github.com/" + releaseRepo + "/releases/download/" + version + "/"
	assets := map[string]string{}
	for _, a := range r.Assets {
		if a.URL != prefix+a.Name {
			return errors.New("unexpected release source")
		}
		if _, exists := assets[a.Name]; exists {
			return errors.New("duplicate release asset")
		}
		assets[a.Name] = a.URL
	}
	for _, platform := range []string{"linux_amd64", "linux_arm64", "darwin_amd64", "darwin_arm64"} {
		if assets["mobterm-cli_"+version+"_"+platform+".tar.gz"] == "" {
			return errors.New("release is incomplete")
		}
	}
	name := "mobterm-cli_" + version + "_" + runtime.GOOS + "_" + runtime.GOARCH + ".tar.gz"
	if assets["checksums.txt"] == "" {
		return errors.New("release checksum unavailable")
	}
	progress.begin("Downloading checksums", "Checksums")
	checks, e := download(ctx, c, assets["checksums.txt"], 16384, progress)
	if e != nil {
		return e
	}
	progress.begin("Downloading package", "Package")
	archive, e := download(ctx, c, assets[name], 64*1024*1024, progress)
	if e != nil {
		return e
	}
	progress.begin("Verifying SHA-256", "SHA-256")
	if e = verifyChecksum(archive, checks, name); e != nil {
		return e
	}
	progress.begin("Extracting executable", "Extract")
	binary, e := extractBinary(archive)
	if e != nil {
		return e
	}
	progress.begin("Checking executable platform", "Platform")
	if e = verifyExecutable(binary, runtime.GOOS, runtime.GOARCH); e != nil {
		return e
	}
	if e := ctx.Err(); e != nil {
		return e
	}
	progress.begin("Replacing executable", "Replace")
	info, e := os.Lstat(destination)
	if e != nil || !info.Mode().IsRegular() {
		return errors.New("upgrade destination must be current regular executable")
	}
	return atomicWrite(destination, binary, info.Mode().Perm())
}
