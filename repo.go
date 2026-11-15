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
		fmt.Fprintf(os.Stderr, "\r|%s%s\x1b[0m| %3.0f%%",
			strings.Repeat("\x1b[32m~", filled),
			strings.Repeat("\x1b[31m-", 20-filled),
			ratio*100)
	} else {
		fmt.Fprintf(os.Stderr, "\rDownloaded %d bytes", p.downloaded)
	}
	p.lastUpdate = time.Now()
}

func (p *downloadProgress) finish() {
	if p.enabled {
		p.render()
		fmt.Fprintln(os.Stderr)
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
		enabled: term.IsTerminal(int(os.Stderr.Fd())),
	}

	_, copyErr := io.Copy(io.MultiWriter(tmp, progress), resp.Body)
	progress.finish()
	if copyErr != nil {
		tmp.Close()
		return fmt.Errorf("copy %s: %w", src, copyErr)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}

	return os.Rename(tmp.Name(), dest)
}

func repodbpath(repo string) string {
	cachedir, e := os.UserCacheDir()
	if e != nil {
		cachedir = "."
	}
	return filepath.Join(cachedir, "stowy", repo+".db")
}
