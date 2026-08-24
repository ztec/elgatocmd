// Package selfupdate downloads and atomically installs verified release binaries.
package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"
)

const (
	DefaultReleaseAPI = "https://git2.riper.fr/api/v1/repos/ztec/elgatocmd"
	maxReleaseBytes   = 4 << 20
	maxChecksumBytes  = 1 << 20
	maxArchiveBytes   = 100 << 20
	maxBinaryBytes    = 50 << 20
)

// Config supplies the current runtime identity and optional test overrides.
type Config struct {
	ReleaseAPI     string
	CurrentVersion string
	ExecutablePath string
	GOOS           string
	GOARCH         string
	Force          bool
	HTTPClient     *http.Client
	Progressf      func(string, ...any)
}

// Result describes the selected release and whether the executable changed.
type Result struct {
	PreviousVersion string `json:"previousVersion"`
	Version         string `json:"version"`
	Channel         string `json:"channel"`
	ExecutablePath  string `json:"executablePath"`
	Changed         bool   `json:"changed"`
}

type release struct {
	TagName    string  `json:"tag_name"`
	Prerelease bool    `json:"prerelease"`
	Draft      bool    `json:"draft"`
	Assets     []asset `json:"assets"`
}

type asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

type httpStatusError struct {
	URL        string
	StatusCode int
	Status     string
}

func (e *httpStatusError) Error() string {
	return fmt.Sprintf("fetch %s: HTTP %s", e.URL, e.Status)
}

// Run selects the latest release, verifies its archive, and replaces the
// resolved current executable from a temporary file in the same directory.
func Run(ctx context.Context, config Config) (Result, error) {
	if config.ReleaseAPI == "" {
		config.ReleaseAPI = DefaultReleaseAPI
	}
	baseURL, err := parseReleaseAPI(config.ReleaseAPI)
	if err != nil {
		return Result{}, err
	}
	if config.CurrentVersion == "" {
		return Result{}, errors.New("current version is required")
	}
	if config.ExecutablePath == "" {
		config.ExecutablePath, err = os.Executable()
		if err != nil {
			return Result{}, fmt.Errorf("locate current executable: %w", err)
		}
	}
	executablePath, err := filepath.EvalSymlinks(config.ExecutablePath)
	if err != nil {
		return Result{}, fmt.Errorf("resolve current executable: %w", err)
	}
	executablePath, err = filepath.Abs(executablePath)
	if err != nil {
		return Result{}, fmt.Errorf("resolve absolute executable path: %w", err)
	}
	info, err := os.Stat(executablePath)
	if err != nil {
		return Result{}, fmt.Errorf("inspect current executable: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return Result{}, errors.New("current executable must be a regular executable file")
	}
	if config.GOOS == "" || config.GOARCH == "" {
		return Result{}, errors.New("current operating system and architecture are required")
	}
	target, err := releaseTarget(config.GOOS, config.GOARCH)
	if err != nil {
		return Result{}, err
	}
	if config.HTTPClient == nil {
		config.HTTPClient = &http.Client{Timeout: 2 * time.Minute}
	}
	if config.Progressf == nil {
		config.Progressf = func(string, ...any) {}
	}

	selected, channel, err := selectRelease(ctx, config.HTTPClient, baseURL)
	if err != nil {
		return Result{}, err
	}
	result := Result{
		PreviousVersion: config.CurrentVersion,
		Version:         selected.TagName,
		Channel:         channel,
		ExecutablePath:  executablePath,
	}
	config.Progressf("Selected %s %s", channel, selected.TagName)
	if selected.TagName == config.CurrentVersion && !config.Force {
		return result, nil
	}

	archiveAsset, checksumAsset, err := selectAssets(selected.Assets, target)
	if err != nil {
		return Result{}, fmt.Errorf("select assets for release %s: %w", selected.TagName, err)
	}
	archiveURL, err := resolveAssetURL(baseURL, archiveAsset.BrowserDownloadURL)
	if err != nil {
		return Result{}, fmt.Errorf("resolve archive URL: %w", err)
	}
	checksumURL, err := resolveAssetURL(baseURL, checksumAsset.BrowserDownloadURL)
	if err != nil {
		return Result{}, fmt.Errorf("resolve checksum URL: %w", err)
	}
	config.Progressf("Downloading %s", assetName(archiveAsset))
	archive, err := fetch(ctx, config.HTTPClient, archiveURL, maxArchiveBytes)
	if err != nil {
		return Result{}, fmt.Errorf("download release archive: %w", err)
	}
	checksums, err := fetch(ctx, config.HTTPClient, checksumURL, maxChecksumBytes)
	if err != nil {
		return Result{}, fmt.Errorf("download release checksums: %w", err)
	}
	config.Progressf("Verifying SHA-256 checksum")
	if err := verifyChecksum(archive, checksums, assetName(archiveAsset)); err != nil {
		return Result{}, err
	}
	binary, err := extractBinary(archive)
	if err != nil {
		return Result{}, fmt.Errorf("extract release binary: %w", err)
	}
	config.Progressf("Installing %s", executablePath)
	if err := replaceExecutable(ctx, executablePath, info.Mode().Perm(), binary, selected.TagName); err != nil {
		return Result{}, err
	}
	result.Changed = true
	return result, nil
}

func parseReleaseAPI(value string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimRight(value, "/"))
	if err != nil {
		return nil, fmt.Errorf("parse release API URL: %w", err)
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("release API must be an HTTP(S) base URL without a query or fragment")
	}
	return parsed, nil
}

func releaseTarget(goos, goarch string) (string, error) {
	switch goos {
	case "linux":
		switch goarch {
		case "amd64", "arm64":
			return goos + "-" + goarch, nil
		case "arm":
			return "linux-armv7", nil
		}
	case "darwin":
		if goarch == "amd64" || goarch == "arm64" {
			return goos + "-" + goarch, nil
		}
	}
	return "", fmt.Errorf("self-update is not available for %s/%s", goos, goarch)
}

func selectRelease(ctx context.Context, client *http.Client, baseURL *url.URL) (release, string, error) {
	stableURL := strings.TrimRight(baseURL.String(), "/") + "/releases/latest"
	var stable release
	err := fetchJSON(ctx, client, stableURL, &stable)
	if err == nil {
		if stable.TagName == "" {
			return release{}, "", errors.New("latest stable release omitted tag_name")
		}
		return stable, "stable release", nil
	}
	var statusErr *httpStatusError
	if !errors.As(err, &statusErr) || statusErr.StatusCode != http.StatusNotFound {
		return release{}, "", fmt.Errorf("find latest stable release: %w", err)
	}

	prereleaseURL := strings.TrimRight(baseURL.String(), "/") + "/releases?pre-release=true&draft=false&limit=20"
	var candidates []release
	if err := fetchJSON(ctx, client, prereleaseURL, &candidates); err != nil {
		return release{}, "", fmt.Errorf("find latest pre-release: %w", err)
	}
	for _, candidate := range candidates {
		if candidate.Prerelease && !candidate.Draft && candidate.TagName != "" {
			return candidate, "pre-release", nil
		}
	}
	return release{}, "", errors.New("no stable release or non-draft pre-release is available")
}

func fetchJSON(ctx context.Context, client *http.Client, requestURL string, destination any) error {
	body, err := fetch(ctx, client, requestURL, maxReleaseBytes)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, destination); err != nil {
		return fmt.Errorf("decode %s: %w", requestURL, err)
	}
	return nil
}

func fetch(ctx context.Context, client *http.Client, requestURL string, maximum int64) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
		return nil, &httpStatusError{URL: requestURL, StatusCode: response.StatusCode, Status: response.Status}
	}
	if response.ContentLength > maximum {
		return nil, fmt.Errorf("response from %s exceeds %d bytes", requestURL, maximum)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maximum+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maximum {
		return nil, fmt.Errorf("response from %s exceeds %d bytes", requestURL, maximum)
	}
	return body, nil
}

func selectAssets(assets []asset, target string) (asset, asset, error) {
	archiveSuffix := "-" + target + ".tar.gz"
	var archiveAsset, checksumAsset asset
	for _, candidate := range assets {
		name := assetName(candidate)
		if archiveAsset.BrowserDownloadURL == "" && strings.HasSuffix(name, archiveSuffix) {
			archiveAsset = candidate
		}
		if checksumAsset.BrowserDownloadURL == "" && strings.HasSuffix(name, "-checksums.txt") {
			checksumAsset = candidate
		}
	}
	if archiveAsset.BrowserDownloadURL == "" {
		return asset{}, asset{}, fmt.Errorf("release has no %s archive", target)
	}
	if checksumAsset.BrowserDownloadURL == "" {
		return asset{}, asset{}, errors.New("release has no checksum file")
	}
	return archiveAsset, checksumAsset, nil
}

func assetName(value asset) string {
	if value.Name != "" {
		return value.Name
	}
	parsed, err := url.Parse(value.BrowserDownloadURL)
	if err != nil {
		return ""
	}
	return path.Base(parsed.Path)
}

func resolveAssetURL(baseURL *url.URL, value string) (string, error) {
	parsed, err := url.Parse(value)
	if err != nil {
		return "", err
	}
	resolved := baseURL.ResolveReference(parsed)
	if (resolved.Scheme != "http" && resolved.Scheme != "https") || resolved.Host == "" {
		return "", errors.New("asset URL must use HTTP(S)")
	}
	return resolved.String(), nil
}

func verifyChecksum(archive, checksums []byte, archiveName string) error {
	var expected string
	for _, line := range strings.Split(string(checksums), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && path.Base(strings.TrimPrefix(fields[len(fields)-1], "*")) == archiveName {
			expected = strings.ToLower(fields[0])
			break
		}
	}
	decoded, err := hex.DecodeString(expected)
	if expected == "" || err != nil || len(decoded) != sha256.Size {
		return fmt.Errorf("checksum file does not contain a valid SHA-256 for %s", archiveName)
	}
	actual := sha256.Sum256(archive)
	if !bytes.Equal(actual[:], decoded) {
		return fmt.Errorf("SHA-256 verification failed for %s", archiveName)
	}
	return nil
}

func extractBinary(archive []byte) ([]byte, error) {
	gzipReader, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	defer gzipReader.Close()
	tarReader := tar.NewReader(gzipReader)
	var binary []byte
	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		cleanName := path.Clean(header.Name)
		if path.IsAbs(cleanName) || cleanName == ".." || strings.HasPrefix(cleanName, "../") {
			return nil, fmt.Errorf("archive contains unsafe path %q", header.Name)
		}
		if path.Base(cleanName) != "elgatolight" {
			continue
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			return nil, errors.New("archive elgatolight entry is not a regular file")
		}
		if binary != nil {
			return nil, errors.New("archive contains multiple elgatolight binaries")
		}
		if header.Size <= 0 || header.Size > maxBinaryBytes {
			return nil, fmt.Errorf("archive binary size %d is invalid", header.Size)
		}
		binary, err = io.ReadAll(io.LimitReader(tarReader, maxBinaryBytes+1))
		if err != nil {
			return nil, err
		}
		if int64(len(binary)) != header.Size {
			return nil, errors.New("archive binary is truncated")
		}
	}
	if binary == nil {
		return nil, errors.New("archive does not contain elgatolight")
	}
	return binary, nil
}

func replaceExecutable(ctx context.Context, executablePath string, mode os.FileMode, binary []byte, version string) error {
	directory := filepath.Dir(executablePath)
	temporary, err := os.CreateTemp(directory, ".elgatolight-update-*")
	if err != nil {
		return fmt.Errorf("create update beside %s: %w", executablePath, err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err := temporary.Write(binary); err != nil {
		temporary.Close()
		return fmt.Errorf("write downloaded executable: %w", err)
	}
	if err := temporary.Chmod(mode); err != nil {
		temporary.Close()
		return fmt.Errorf("set downloaded executable mode: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync downloaded executable: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close downloaded executable: %w", err)
	}

	verifyCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	output, err := exec.CommandContext(verifyCtx, temporaryPath, "--version").Output()
	if err != nil {
		return fmt.Errorf("run downloaded executable version check: %w", err)
	}
	reported := strings.TrimSuffix(strings.TrimSuffix(string(output), "\n"), "\r")
	if reported != version {
		return fmt.Errorf("downloaded executable reports version %q, expected %q", reported, version)
	}
	if err := os.Rename(temporaryPath, executablePath); err != nil {
		return fmt.Errorf("replace executable %s: %w", executablePath, err)
	}
	return nil
}
