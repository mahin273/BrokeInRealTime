package updater

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	RepoOwner = "mahin273"
	RepoName  = "BrokeInRealTime"
)

// ReleaseInfo holds GitHub release metadata.
type ReleaseInfo struct {
	TagName string  `json:"tag_name"`
	Name    string  `json:"name"`
	Assets  []Asset `json:"assets"`
}

// Asset represents a downloadable release artifact.
type Asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// FetchLatestRelease queries the GitHub Releases API for the latest release.
func FetchLatestRelease(client *http.Client) (*ReleaseInfo, error) {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}

	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", RepoOwner, RepoName)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create release request: %w", err)
	}

	req.Header.Set("User-Agent", "BrokeInRealTime-Updater")
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to reach GitHub API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GitHub API returned status %d: %s", resp.StatusCode, string(body))
	}

	var rel ReleaseInfo
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, fmt.Errorf("failed to parse release metadata: %w", err)
	}

	return &rel, nil
}

// FindAsset locates the appropriate tarball for the given OS and architecture.
func FindAsset(rel *ReleaseInfo, goos, goarch string) (*Asset, error) {
	targetName := fmt.Sprintf("broke-daemon-%s-%s.tar.gz", goos, goarch)
	for _, a := range rel.Assets {
		if strings.EqualFold(a.Name, targetName) {
			return &a, nil
		}
	}
	return nil, fmt.Errorf("no matching precompiled asset '%s' found in release %s", targetName, rel.TagName)
}

// ExtractBinaryFromTarGz extracts the 'broke-daemon' binary executable from a .tar.gz stream.
func ExtractBinaryFromTarGz(r io.Reader) ([]byte, error) {
	gzr, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("failed to decompress gzip archive: %w", err)
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("error reading tar archive: %w", err)
		}

		cleanName := filepath.Base(header.Name)
		if cleanName == "broke-daemon" && header.Typeflag != tar.TypeDir {
			return io.ReadAll(tr)
		}
	}

	return nil, fmt.Errorf("binary 'broke-daemon' not found in archive")
}

// ApplyUpdate writes the binary safely to targetPath using atomic rename and restarts background services if active.
func ApplyUpdate(binBytes []byte, targetPath string) error {
	isSelfUpdate := false
	if targetPath == "" {
		execPath, err := os.Executable()
		if err != nil {
			return fmt.Errorf("cannot resolve current executable path: %w", err)
		}
		targetPath = execPath
		isSelfUpdate = true
	}

	// Resolve symlinks if any
	resolvedPath, err := filepath.EvalSymlinks(targetPath)
	if err == nil {
		targetPath = resolvedPath
	}

	dir := filepath.Dir(targetPath)
	tmpFile, err := os.CreateTemp(dir, ".broke-daemon.tmp-*")
	if err != nil {
		return fmt.Errorf("failed to create temporary file in %s: %w", dir, err)
	}
	tmpName := tmpFile.Name()
	defer os.Remove(tmpName)

	if _, err := tmpFile.Write(binBytes); err != nil {
		tmpFile.Close()
		return fmt.Errorf("failed to write updated binary: %w", err)
	}

	if err := tmpFile.Chmod(0755); err != nil {
		tmpFile.Close()
		return fmt.Errorf("failed to set executable permissions: %w", err)
	}
	tmpFile.Close()

	// Atomic replace
	if err := os.Rename(tmpName, targetPath); err != nil {
		return fmt.Errorf("failed to replace %s: %w", targetPath, err)
	}

	// Try restarting background systemd / launchd service if active and self-updating
	if isSelfUpdate {
		restartServiceIfRunning()
	}

	return nil
}

func restartServiceIfRunning() {
	if runtime.GOOS == "linux" {
		if exec.Command("systemctl", "--user", "is-active", "--quiet", "brokeinrealtime").Run() == nil {
			fmt.Println("[Update] Active systemd service detected. Restarting brokeinrealtime.service...")
			if err := exec.Command("systemctl", "--user", "restart", "brokeinrealtime").Run(); err == nil {
				fmt.Println("[Update] Service restarted successfully.")
			} else {
				fmt.Printf("[Update] Warning: failed to restart systemd service: %v\n", err)
			}
		}
	} else if runtime.GOOS == "darwin" {
		home, _ := os.UserHomeDir()
		plist := filepath.Join(home, "Library/LaunchAgents/com.user.brokeinrealtime.plist")
		if _, err := os.Stat(plist); err == nil {
			fmt.Println("[Update] Active macOS launchd agent detected. Reloading agent...")
			_ = exec.Command("launchctl", "unload", plist).Run()
			_ = exec.Command("launchctl", "load", "-w", plist).Run()
			fmt.Println("[Update] Launchd agent reloaded successfully.")
		}
	}
}

// CheckAndApplyUpdate orchestrates checking GitHub, downloading, and replacing the binary.
func CheckAndApplyUpdate(currentVersion string, force bool) error {
	fmt.Printf("[Update] Checking for updates (current version: v%s)...\n", currentVersion)

	client := &http.Client{Timeout: 30 * time.Second}
	rel, err := FetchLatestRelease(client)
	if err != nil {
		return fmt.Errorf("failed to check latest release: %w", err)
	}

	latestTag := rel.TagName
	cleanLatest := strings.TrimPrefix(latestTag, "v")
	cleanCurrent := strings.TrimPrefix(currentVersion, "v")

	if cleanLatest == cleanCurrent && !force {
		fmt.Printf("[Update] BrokeInRealTime is already up to date (v%s).\n", currentVersion)
		return nil
	}

	fmt.Printf("[Update] New version available: v%s -> %s\n", cleanCurrent, latestTag)

	asset, err := FindAsset(rel, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return err
	}

	fmt.Printf("[Update] Downloading asset: %s\n", asset.Name)
	req, err := http.NewRequest("GET", asset.BrowserDownloadURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create download request: %w", err)
	}
	req.Header.Set("User-Agent", "BrokeInRealTime-Updater")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to download release: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed with HTTP %d", resp.StatusCode)
	}

	fmt.Println("[Update] Extracting binary...")
	binBytes, err := ExtractBinaryFromTarGz(resp.Body)
	if err != nil {
		return err
	}

	fmt.Println("[Update] Installing update...")
	if err := ApplyUpdate(binBytes, ""); err != nil {
		return err
	}

	fmt.Printf("[Update] Successfully updated BrokeInRealTime to %s!\n", latestTag)
	return nil
}
