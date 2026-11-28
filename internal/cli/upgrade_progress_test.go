package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

func TestUpgradeDownloadProgress(t *testing.T) {
	for _, test := range []struct {
		name  string
		total int64
		want  string
	}{
		{"known", 8, "50%  4 B / 8 B"},
		{"unknown", -1, "4 B  1s"},
	} {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				var log bytes.Buffer
				caps := terminalCaps{Width: 72, Dynamic: true, Unicode: true}
				p := newUpgradeProgress(&log, func() terminalCaps { return caps }, "auto")
				defer p.finish()
				snapshot := func() string {
					p.mu.Lock()
					defer p.mu.Unlock()
					return log.String()
				}
				p.begin("Downloading package", "Package")
				r, w := io.Pipe()
				client := &http.Client{Transport: releaseTransport(func(req *http.Request) (*http.Response, error) {
					context.AfterFunc(req.Context(), func() { r.CloseWithError(req.Context().Err()) })
					return &http.Response{StatusCode: 200, Body: r, ContentLength: test.total}, nil
				})}
				done := make(chan error, 1)
				go func() {
					_, err := download(ctx, client.Transport, "https://github.com/archive", githubAsset, 8, p)
					done <- err
				}()
				if _, err := w.Write([]byte("abcd")); err != nil {
					t.Fatal(err)
				}
				synctest.Wait()
				if got := snapshot(); got != "" {
					t.Fatal("short download flashed progress", got)
				}
				time.Sleep(time.Second)
				synctest.Wait()
				if got := snapshot(); !strings.Contains(got, test.want) || test.total < 0 && strings.Contains(got, "%") {
					t.Fatalf("expected %q in %q", test.want, got)
				}
				// Resize while the reader is blocked. The ticker still redraws
				// a compact frame, which fits without consuming a second row.
				p.mu.Lock()
				caps.Width = 15
				log.Reset()
				p.mu.Unlock()
				time.Sleep(progressInterval)
				synctest.Wait()
				if got := snapshot(); !strings.HasPrefix(got, "\r\x1b[JPackage ") || strings.Contains(got, "\n") {
					t.Fatalf("resize produced %q", got)
				}
				cancel()
				if err := <-done; !errors.Is(err, context.Canceled) {
					t.Fatalf("cancelled download returned %v", err)
				}
				p.finish()
				if !strings.HasSuffix(log.String(), "\r\x1b[K") {
					t.Fatalf("unfinished progress line %q", log.String())
				}
				n := log.Len()
				time.Sleep(time.Second)
				if log.Len() != n {
					t.Fatal("progress continued after finish")
				}
				w.Close()
			})
		})
	}
}

func TestUpgradeProgressPlainAndNever(t *testing.T) {
	for _, mode := range []string{"plain", "never"} {
		var log bytes.Buffer
		caps := func() terminalCaps { return terminalCaps{Width: 80, Dynamic: true} }
		p := newUpgradeProgress(&log, caps, mode)
		p.begin("Downloading checksums", "Checksums")
		p.startBody(32)
		p.Write([]byte("fixture"))
		p.begin("Verifying SHA-256", "SHA-256")
		p.finish()
		if mode == "never" && log.Len() != 0 || mode == "plain" && log.String() != "Downloading checksums\nVerifying SHA-256\n" {
			t.Fatalf("%s output %q", mode, log.String())
		}
		if p.currentStage() != "Verifying SHA-256" {
			t.Fatal("failure stage lost when progress was disabled")
		}
	}
}
