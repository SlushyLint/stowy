package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"stowy/internal/statusline"

	"golang.org/x/term"
)

type downloadProgress struct {
	total      int64
	downloaded int64
	enabled    bool
	lastUpdate time.Time
}

func (p *downloadProgress) Write(b []byte) (int, error) {
	p.downloaded += int64(len(b))
	if p.enabled && time.Since(p.lastUpdate) >= 100*time.Millisecond {
		p.render()
	}
	return len(b), nil
}

func (p *downloadProgress) render() {

	if p.total > 0 {
		ratio := float64(p.downloaded) / float64(p.total)
		if ratio > 1 {
			ratio = 1
		}
		filled := int(ratio * 20)
		// Draw the download bar in the footer's lower row.
		statusline.Progress(fmt.Sprintf("|\x1b[32m%s\x1b[0m\x1b[90m%s\x1b[0m| %3.0f%%",
			strings.Repeat("~", filled),
			strings.Repeat("-", 20-filled),
			ratio*100))
	} else {
		statusline.Progress(fmt.Sprintf("\x1b[36mDownloaded %d bytes\x1b[0m", p.downloaded))
	}
	p.lastUpdate = time.Now()
}

func (p *downloadProgress) finish() {
	if p.enabled {
		p.render()
		// Clear the lower footer row after the download finishes.
		statusline.FinishProgress()
	}
}

func dlfile(ctx context.Context, src, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src, nil)
	if err != nil {
		return fmt.Errorf("request %s: %w", src, err)
	}

	client := &http.Client{Timeout: 2 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download %s: %w", src, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: %s", src, resp.Status)
	}

	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(dest), err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(dest), ".stowy-dl-*")

	if err != nil {
		return fmt.Errorf("temp file: %w", err)
	}
	defer os.Remove(tmp.Name())

	progress := &downloadProgress{
		total:   resp.ContentLength,
		enabled: term.IsTerminal(int(os.Stdout.Fd())),
	}
	// Keep the operation label above the download bar in the shared footer.
	statusline.Status(fmt.Sprintf("Downloading %s", filepath.Base(src)))

	_, copyErr := io.Copy(io.MultiWriter(tmp, progress), resp.Body)
	progress.finish()
	if copyErr != nil {
		tmp.Close()
		return fmt.Errorf("copy %s: %w", src, copyErr)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}

	if err := os.Rename(tmp.Name(), dest); err != nil {
		return err
	}
	statusline.Status(fmt.Sprintf("Downloaded %s", filepath.Base(dest)))
	return nil
}

func repodbpath(repo string) string {
	cachedir, e := os.UserCacheDir()
	if e != nil {
		cachedir = "."
	}
	return filepath.Join(cachedir, "stowy", repo+".db")
}
