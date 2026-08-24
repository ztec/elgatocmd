package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunInstallsVerifiedStableRelease(t *testing.T) {
	fixture := newReleaseFixture(t, "release/0.1+build", false, "linux-amd64")
	result, executable := fixture.run(t, "old-version", false)
	if !result.Changed || result.PreviousVersion != "old-version" || result.Version != "release/0.1+build" || result.Channel != "stable release" {
		t.Fatalf("result = %#v", result)
	}
	content, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != fixture.binary {
		t.Fatalf("installed binary = %q", content)
	}
	if fixture.prereleaseRequests != 0 || fixture.archiveRequests != 1 || fixture.checksumRequests != 1 {
		t.Fatalf("requests: pre=%d archive=%d checksums=%d", fixture.prereleaseRequests, fixture.archiveRequests, fixture.checksumRequests)
	}
}

func TestRunFallsBackToPrerelease(t *testing.T) {
	fixture := newReleaseFixture(t, "preview-2", true, "linux-amd64")
	result, _ := fixture.run(t, "old-version", false)
	if !result.Changed || result.Version != "preview-2" || result.Channel != "pre-release" {
		t.Fatalf("result = %#v", result)
	}
	if fixture.prereleaseRequests != 1 {
		t.Fatalf("pre-release requests = %d", fixture.prereleaseRequests)
	}
}

func TestRunSkipsExactVersionUnlessForced(t *testing.T) {
	fixture := newReleaseFixture(t, "same-version", false, "linux-amd64")
	result, executable := fixture.run(t, "same-version", false)
	if result.Changed || result.Version != "same-version" {
		t.Fatalf("result = %#v", result)
	}
	if fixture.archiveRequests != 0 || fixture.checksumRequests != 0 {
		t.Fatalf("same version downloaded assets: archive=%d checksums=%d", fixture.archiveRequests, fixture.checksumRequests)
	}
	content, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(content), executableScript("same-version"); got != want {
		t.Fatalf("same-version update replaced executable: %q", content)
	}

	forced, _ := fixture.runAt(t, executable, "same-version", true)
	if !forced.Changed || fixture.archiveRequests != 1 {
		t.Fatalf("forced result = %#v, archive requests = %d", forced, fixture.archiveRequests)
	}
}

func TestRunRejectsBadChecksumWithoutReplacingExecutable(t *testing.T) {
	fixture := newReleaseFixture(t, "new-version", false, "linux-amd64")
	fixture.checksums = []byte(strings.Repeat("0", 64) + "  " + fixture.archiveName + "\n")
	executable := fixture.writeCurrent(t, "old-version")
	_, err := fixture.runAt(t, executable, "old-version", false)
	if err == nil || !strings.Contains(err.Error(), "SHA-256 verification failed") {
		t.Fatalf("checksum error = %v", err)
	}
	content, readErr := os.ReadFile(executable)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !strings.Contains(string(content), "old-version") {
		t.Fatalf("failed update replaced executable: %q", content)
	}
}

func TestRunRejectsMismatchedEmbeddedVersion(t *testing.T) {
	fixture := newReleaseFixture(t, "published-version", false, "linux-amd64")
	fixture.binary = executableScript("different-version")
	fixture.rebuildArchive(t)
	executable := fixture.writeCurrent(t, "old-version")
	_, err := fixture.runAt(t, executable, "old-version", false)
	if err == nil || !strings.Contains(err.Error(), `reports version "different-version", expected "published-version"`) {
		t.Fatalf("version error = %v", err)
	}
	content, readErr := os.ReadFile(executable)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !strings.Contains(string(content), "old-version") {
		t.Fatalf("mismatched update replaced executable: %q", content)
	}
}

func TestReleaseTargetMatchesPublishedMatrix(t *testing.T) {
	tests := map[string]string{
		"linux/amd64": "linux-amd64",
		"linux/arm64": "linux-arm64",
		"linux/arm":   "linux-armv7",
	}
	for platform, want := range tests {
		parts := strings.Split(platform, "/")
		got, err := releaseTarget(parts[0], parts[1])
		if err != nil || got != want {
			t.Errorf("releaseTarget(%s) = %q, %v; want %q", platform, got, err, want)
		}
	}
	for _, platform := range []string{"darwin/amd64", "darwin/arm64", "windows/amd64"} {
		parts := strings.Split(platform, "/")
		if _, err := releaseTarget(parts[0], parts[1]); err == nil {
			t.Fatalf("self-update unexpectedly supported on %s", platform)
		}
	}
}

func TestExtractBinaryRejectsSymlinkEntry(t *testing.T) {
	var compressed bytes.Buffer
	gzipWriter := gzip.NewWriter(&compressed)
	tarWriter := tar.NewWriter(gzipWriter)
	if err := tarWriter.WriteHeader(&tar.Header{Name: "package/elgatolight", Typeflag: tar.TypeSymlink, Linkname: "/tmp/other"}); err != nil {
		t.Fatal(err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := extractBinary(compressed.Bytes()); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("symlink archive error = %v", err)
	}
}

type releaseFixture struct {
	t                  *testing.T
	tag                string
	prerelease         bool
	target             string
	binary             string
	archiveName        string
	checksumName       string
	archive            []byte
	checksums          []byte
	server             *httptest.Server
	prereleaseRequests int
	archiveRequests    int
	checksumRequests   int
}

func newReleaseFixture(t *testing.T, tag string, prerelease bool, target string) *releaseFixture {
	t.Helper()
	fixture := &releaseFixture{
		t: t, tag: tag, prerelease: prerelease, target: target,
		binary: executableScript(tag), archiveName: "elgatolight-test-" + target + ".tar.gz",
		checksumName: "elgatolight-test-checksums.txt",
	}
	fixture.rebuildArchive(t)
	fixture.server = httptest.NewServer(http.HandlerFunc(fixture.serveHTTP))
	t.Cleanup(fixture.server.Close)
	return fixture
}

func (f *releaseFixture) rebuildArchive(t *testing.T) {
	t.Helper()
	var compressed bytes.Buffer
	gzipWriter := gzip.NewWriter(&compressed)
	tarWriter := tar.NewWriter(gzipWriter)
	header := &tar.Header{Name: "elgatolight-test-" + f.target + "/elgatolight", Mode: 0o755, Size: int64(len(f.binary)), Typeflag: tar.TypeReg}
	if err := tarWriter.WriteHeader(header); err != nil {
		t.Fatal(err)
	}
	if _, err := tarWriter.Write([]byte(f.binary)); err != nil {
		t.Fatal(err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	f.archive = compressed.Bytes()
	sum := sha256.Sum256(f.archive)
	f.checksums = []byte(fmt.Sprintf("%x  %s\n", sum, f.archiveName))
}

func (f *releaseFixture) serveHTTP(writer http.ResponseWriter, request *http.Request) {
	switch request.URL.Path {
	case "/api/releases/latest":
		if f.prerelease {
			http.NotFound(writer, request)
			return
		}
		_ = json.NewEncoder(writer).Encode(f.releaseResponse())
	case "/api/releases":
		f.prereleaseRequests++
		_ = json.NewEncoder(writer).Encode([]release{f.releaseResponse()})
	case "/downloads/" + f.archiveName:
		f.archiveRequests++
		_, _ = writer.Write(f.archive)
	case "/downloads/" + f.checksumName:
		f.checksumRequests++
		_, _ = writer.Write(f.checksums)
	default:
		http.NotFound(writer, request)
	}
}

func (f *releaseFixture) releaseResponse() release {
	return release{
		TagName: f.tag, Prerelease: f.prerelease,
		Assets: []asset{
			{Name: f.archiveName, BrowserDownloadURL: f.server.URL + "/downloads/" + f.archiveName},
			{Name: f.checksumName, BrowserDownloadURL: f.server.URL + "/downloads/" + f.checksumName},
		},
	}
}

func (f *releaseFixture) run(t *testing.T, current string, force bool) (Result, string) {
	t.Helper()
	executable := f.writeCurrent(t, current)
	result, err := f.runAt(t, executable, current, force)
	if err != nil {
		t.Fatal(err)
	}
	return result, executable
}

func (f *releaseFixture) runAt(t *testing.T, executable, current string, force bool) (Result, error) {
	t.Helper()
	return Run(context.Background(), Config{
		ReleaseAPI: f.server.URL + "/api", CurrentVersion: current, ExecutablePath: executable,
		GOOS: "linux", GOARCH: "amd64", Force: force, HTTPClient: f.server.Client(),
	})
}

func (f *releaseFixture) writeCurrent(t *testing.T, version string) string {
	t.Helper()
	directory := t.TempDir()
	executable := filepath.Join(directory, "elgatolight")
	if err := os.WriteFile(executable, []byte(executableScript(version)), 0o755); err != nil {
		t.Fatal(err)
	}
	return executable
}

func executableScript(version string) string {
	return "#!/bin/sh\nprintf '%s\\n' '" + version + "'\n"
}
