package render

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// ErrNoBrowser: no Chromium was found to print the PDF.
var ErrNoBrowser = errors.New("render: no Chromium or Chrome found for PDF (set Options.Chrome or RENDER_CHROME)")

var browsers = []string{"chromium", "chromium-browser", "google-chrome", "google-chrome-stable", "chrome", "chrome-headless-shell"}

// FindChrome returns the browser PDF would use, or "".
func FindChrome(o Options) string {
	for _, c := range []string{o.Chrome, os.Getenv("RENDER_CHROME")} {
		if c != "" {
			if _, err := os.Stat(c); err == nil {
				return c
			}
		}
	}
	for _, name := range browsers {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	return ""
}

// PDF renders Markdown as a PDF by printing the HTML page with a headless browser. It is
// bounded by the context (and by 60 seconds if the context has no deadline). The browser
// runs with no network, in a throwaway profile, and cannot read anything but the one page.
func PDF(ctx context.Context, markdown string, o Options) ([]byte, error) {
	page, err := HTML(markdown, o)
	if err != nil {
		return nil, err
	}
	chrome := FindChrome(o)
	if chrome == "" {
		return nil, ErrNoBrowser
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
	}
	dir, err := os.MkdirTemp("", "render-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	src, dst := filepath.Join(dir, "doc.html"), filepath.Join(dir, "doc.pdf")
	if err := os.WriteFile(src, page, 0o600); err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, chrome,
		"--headless", "--disable-gpu", "--no-sandbox", "--hide-scrollbars",
		"--user-data-dir="+filepath.Join(dir, "profile"),
		"--no-pdf-header-footer", "--print-to-pdf="+dst,
		// nothing may be fetched, whatever the page contains
		"--host-resolver-rules=MAP * ~NOTFOUND",
		"file://"+src)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("render: the PDF took too long: %w", ctx.Err())
		}
		return nil, fmt.Errorf("render: the browser failed: %w: %.300s", err, stderr.String())
	}
	b, err := os.ReadFile(dst)
	if err != nil {
		return nil, fmt.Errorf("render: the browser made no PDF: %w", err)
	}
	if !bytes.HasPrefix(b, []byte("%PDF-")) {
		return nil, errors.New("render: the browser's output is not a PDF")
	}
	return b, nil
}
