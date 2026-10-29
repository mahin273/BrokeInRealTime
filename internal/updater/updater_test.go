package updater

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func TestFindAsset(t *testing.T) {
	rel := &ReleaseInfo{
		TagName: "v1.2.1",
		Assets: []Asset{
			{Name: "broke-daemon-linux-amd64.tar.gz", BrowserDownloadURL: "https://example.com/linux"},
			{Name: "broke-daemon-darwin-arm64.tar.gz", BrowserDownloadURL: "https://example.com/darwin"},
		},
	}

	asset, err := FindAsset(rel, "linux", "amd64")
	if err != nil {
		t.Fatalf("unexpected error finding asset: %v", err)
	}
	if asset.Name != "broke-daemon-linux-amd64.tar.gz" {
		t.Errorf("expected linux amd64 asset, got: %s", asset.Name)
	}

	_, err = FindAsset(rel, "windows", "arm64")
	if err == nil {
		t.Errorf("expected error for missing windows asset, got nil")
	}
}

func TestExtractBinaryFromTarGz(t *testing.T) {
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	expectedPayload := []byte("#!/bin/sh\necho 'hello'")
	hdr := &tar.Header{
		Name: "broke-daemon",
		Mode: 0755,
		Size: int64(len(expectedPayload)),
	}

	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatalf("WriteHeader failed: %v", err)
	}
	if _, err := tw.Write(expectedPayload); err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	tw.Close()
	gw.Close()

	extracted, err := ExtractBinaryFromTarGz(&buf)
	if err != nil {
		t.Fatalf("ExtractBinaryFromTarGz failed: %v", err)
	}
	if !bytes.Equal(extracted, expectedPayload) {
		t.Fatalf("extracted payload mismatch: expected %q, got %q", expectedPayload, extracted)
	}
}

func TestApplyUpdate(t *testing.T) {
	tmpDir := t.TempDir()
	targetFile := filepath.Join(tmpDir, "broke-daemon")

	// Create initial file
	if err := os.WriteFile(targetFile, []byte("v1"), 0755); err != nil {
		t.Fatalf("failed to create initial file: %v", err)
	}

	// Apply update
	newPayload := []byte("v2-updated-binary-content")
	if err := ApplyUpdate(newPayload, targetFile); err != nil {
		t.Fatalf("ApplyUpdate failed: %v", err)
	}

	content, err := os.ReadFile(targetFile)
	if err != nil {
		t.Fatalf("failed to read updated file: %v", err)
	}
	if !bytes.Equal(content, newPayload) {
		t.Fatalf("expected updated content %q, got %q", newPayload, content)
	}

	info, err := os.Stat(targetFile)
	if err != nil {
		t.Fatalf("failed to stat file: %v", err)
	}
	if info.Mode().Perm()&0111 == 0 {
		t.Errorf("expected file to be executable, got perm: %v", info.Mode().Perm())
	}
}
