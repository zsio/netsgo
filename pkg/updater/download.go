package updater

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

func downloadAndExtract(url, destPath string, client *http.Client) error {
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download: status %d", resp.StatusCode)
	}
	return extractBinary(resp.Body, destPath)
}

func extractBinary(r io.Reader, destPath string) error {
	gr, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("extract: open gzip: %w", err)
	}
	defer func() { _ = gr.Close() }()

	tr := tar.NewReader(gr)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("extract: read tar: %w", err)
		}
		clean := filepath.Clean(header.Name)
		if clean == "netsgo" || clean == "bin/netsgo" {
			_ = os.MkdirAll(filepath.Dir(destPath), 0o755)
			f, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
			if err != nil {
				return fmt.Errorf("extract: create file: %w", err)
			}
			if _, err := io.Copy(f, tr); err != nil {
				_ = f.Close()
				return fmt.Errorf("extract: write: %w", err)
			}
			if err := f.Close(); err != nil {
				return fmt.Errorf("extract: close: %w", err)
			}
			return nil
		}
	}
	return fmt.Errorf("extract: binary 'netsgo' not found")
}
