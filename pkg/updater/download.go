package updater

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func downloadAndExtract(url, destPath string, client *http.Client) error {
	archiveData, err := downloadReleaseAsset(url, client)
	if err != nil {
		return err
	}

	if err := verifyReleaseAsset(url, archiveData, client); err != nil {
		return err
	}

	return extractBinary(bytes.NewReader(archiveData), destPath)
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
		if !isExtractableBinaryEntry(header) {
			continue
		}
		if isArchiveBinaryPath(header.Name) {
			if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
				return fmt.Errorf("extract: mkdir: %w", err)
			}
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

func downloadReleaseAsset(url string, client *http.Client) ([]byte, error) {
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("download: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download: status %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("download: read body: %w", err)
	}

	return data, nil
}

func verifyReleaseAsset(assetURL string, archiveData []byte, client *http.Client) error {
	checksumURL, assetName, err := checksumsURLForAsset(assetURL)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}

	manifestData, err := downloadChecksumsFile(checksumURL, client)
	if err != nil {
		return err
	}

	expected, err := parseChecksumManifest(manifestData, assetName)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}

	actual := sha256.Sum256(archiveData)
	actualHex := hex.EncodeToString(actual[:])
	if actualHex != expected {
		return fmt.Errorf("download: checksum mismatch for %s", assetName)
	}

	return nil
}

func downloadChecksumsFile(url string, client *http.Client) ([]byte, error) {
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("download checksums: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download checksums: status %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("download checksums: read body: %w", err)
	}

	return data, nil
}

func checksumsURLForAsset(assetURL string) (string, string, error) {
	lastSlash := strings.LastIndex(assetURL, "/")
	if lastSlash == -1 || lastSlash == len(assetURL)-1 {
		return "", "", fmt.Errorf("invalid asset url: %q", assetURL)
	}

	assetName := assetURL[lastSlash+1:]
	return assetURL[:lastSlash+1] + checksumsAsset, assetName, nil
}

func parseChecksumManifest(manifest []byte, assetName string) (string, error) {
	for _, line := range strings.Split(string(manifest), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if line == "" {
			continue
		}

		parts := strings.SplitN(line, "  ", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return "", fmt.Errorf("invalid checksums.txt line: %q", line)
		}
		if parts[1] != assetName {
			continue
		}
		if len(parts[0]) != sha256.Size*2 {
			return "", fmt.Errorf("invalid checksum length for %s", assetName)
		}
		if _, err := hex.DecodeString(parts[0]); err != nil {
			return "", fmt.Errorf("invalid checksum for %s: %w", assetName, err)
		}
		return strings.ToLower(parts[0]), nil
	}

	return "", fmt.Errorf("checksum for %s not found in checksums.txt", assetName)
}

func isArchiveBinaryPath(name string) bool {
	clean := filepath.Clean(name)
	if clean == "." || clean == ".." || filepath.IsAbs(clean) {
		return false
	}
	if clean == "netsgo" {
		return true
	}

	parts := strings.Split(clean, string(filepath.Separator))
	if len(parts) < 2 {
		return false
	}
	for _, part := range parts {
		if part == ".." || part == "" {
			return false
		}
	}

	return parts[len(parts)-1] == "netsgo"
}

func isExtractableBinaryEntry(header *tar.Header) bool {
	if header == nil {
		return false
	}
	if header.Typeflag != tar.TypeReg {
		return false
	}
	return isArchiveBinaryPath(header.Name)
}
