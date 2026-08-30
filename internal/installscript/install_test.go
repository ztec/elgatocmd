// Package installscript tests the public shell installer as an external user sees it.
package installscript

import (
	"archive/tar"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

const fakeCurl = `#!/bin/sh
set -eu
url=
output=
while [ "$#" -gt 0 ]; do
	case $1 in
	--proto|--proto-redir|--connect-timeout|--max-time|-o)
		option=$1
		value=$2
		shift 2
		[ "$option" = -o ] && output=$value
		;;
	-*) shift ;;
	*) url=$1; shift ;;
	esac
done
case $url in
*/releases/latest)
	if [ "${FAKE_NO_STABLE:-0}" = 1 ]; then
		printf 'curl: expected missing stable release\n' >&2
		exit 22
	fi
	cp "$FAKE_RELEASE" "$output"
	;;
*/releases?pre-release=*) cp "$FAKE_RELEASE" "$output" ;;
*/central-key-api/contents/release-keys?ref=main) cp "$FAKE_KEYS_INDEX" "$output" ;;
*/central-key-repository/raw/branch/main/release-keys/2026-08-26.pub) cp "$FAKE_ACTIVE_KEY" "$output" ;;
*/central-key-repository/raw/branch/main/release-keys/2026-08-25-revoked.pub) cp "$FAKE_REVOKED_KEY" "$output" ;;
*-checksums.txt.sig) cp "$FAKE_SIGNATURE" "$output" ;;
*-checksums.txt) cp "$FAKE_CHECKSUMS" "$output" ;;
*.tar.gz) cp "$FAKE_ARCHIVE" "$output" ;;
*) exit 22 ;;
esac
`

type installerFixture struct {
	root            string
	archive         string
	checksums       string
	signature       string
	release         string
	keysIndex       string
	activeKey       string
	revokedKey      string
	fakeBin         string
	installDir      string
	archiveName     string
	expectedVersion string
	noStable        bool
}

func TestInstallerVerifiesSignatureAndAtomicallyInstalls(t *testing.T) {
	fixture := newInstallerFixture(t, false)
	output, err := fixture.run(t, false)
	if err != nil {
		t.Fatalf("installer failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "Signature verified for v0.1") || !strings.Contains(string(output), "Installed Elgato Key Light Neo USB controller v0.1") {
		t.Fatalf("installer output = %q", output)
	}
	installed := filepath.Join(fixture.installDir, "elgatolight")
	info, err := os.Stat(installed)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("installed mode = %o", info.Mode().Perm())
	}
	version, err := exec.Command(installed, "--version").Output()
	if err != nil || strings.TrimSpace(string(version)) != fixture.expectedVersion {
		t.Fatalf("installed version = %q, %v", version, err)
	}
}

func TestInstallerCleanlyFallsBackToPrerelease(t *testing.T) {
	fixture := newInstallerFixture(t, false)
	fixture.noStable = true
	output, err := fixture.run(t, false)
	if err != nil {
		t.Fatalf("prerelease fallback failed: %v\n%s", err, output)
	}
	message := string(output)
	for _, required := range []string{"No stable release found", "Signature verified for v0.1", "Installed Elgato Key Light Neo USB controller v0.1"} {
		if !strings.Contains(message, required) {
			t.Fatalf("prerelease fallback output does not contain %q: %q", required, message)
		}
	}
	if strings.Contains(message, "curl:") {
		t.Fatalf("prerelease fallback leaked the expected stable-release probe error: %q", message)
	}
}

func TestInstallerExplainsRevokedReleaseSigner(t *testing.T) {
	fixture := newInstallerFixture(t, true)
	output, err := fixture.run(t, false)
	if err == nil {
		t.Fatalf("installer accepted a revoked signer:\n%s", output)
	}
	message := string(output)
	if !strings.Contains(message, "revoked key 2026-08-25-revoked.pub") || !strings.Contains(message, "newer release") || !strings.Contains(message, "run the installer again") {
		t.Fatalf("revoked-key error = %q", message)
	}
	if _, err := os.Stat(filepath.Join(fixture.installDir, "elgatolight")); !os.IsNotExist(err) {
		t.Fatalf("revoked release created an installed binary: %v", err)
	}
}

func TestInstallerAllowsExplicitNonInteractiveChecksumOnlyInstall(t *testing.T) {
	fixture := newInstallerFixture(t, false)
	output, err := fixture.run(t, true, "--skip-signature-verification")
	if err != nil {
		t.Fatalf("checksum-only installer failed: %v\n%s", err, output)
	}
	message := string(output)
	for _, required := range []string{
		"Ed25519 signature verification is disabled",
		"cannot prove authenticity",
		"Checksum verified for " + fixture.archiveName,
		"Installed Elgato Key Light Neo USB controller v0.1",
	} {
		if !strings.Contains(message, required) {
			t.Fatalf("checksum-only output does not contain %q: %q", required, message)
		}
	}
}

func TestInstallerWithoutOpenSSLRequiresExplicitBypassOutsideTerminal(t *testing.T) {
	fixture := newInstallerFixture(t, false)
	output, err := fixture.run(t, true)
	if err == nil {
		t.Fatalf("installer continued without OpenSSL or confirmation:\n%s", output)
	}
	message := string(output)
	if !strings.Contains(message, "signature cannot be verified") || !strings.Contains(message, "--skip-signature-verification") {
		t.Fatalf("missing-OpenSSL output = %q", message)
	}
}

func newInstallerFixture(t *testing.T, signWithRevokedKey bool) installerFixture {
	t.Helper()
	root := t.TempDir()
	archiveName := "elgatolight-v0.1-linux-amd64.tar.gz"
	archivePath := filepath.Join(root, archiveName)
	writeArchive(t, archivePath, "v0.1")
	archive, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(archive)
	checksumsPath := filepath.Join(root, "elgatolight-v0.1-checksums.txt")
	checksums := []byte(fmt.Sprintf("%x  %s\n", digest, archiveName))
	if err := os.WriteFile(checksumsPath, checksums, 0o644); err != nil {
		t.Fatal(err)
	}

	activePublic, activePrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	revokedPublic, revokedPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer := activePrivate
	if signWithRevokedKey {
		signer = revokedPrivate
	}
	signaturePath := filepath.Join(root, "elgatolight-v0.1-checksums.txt.sig")
	signature := base64.StdEncoding.EncodeToString(ed25519.Sign(signer, checksums)) + "\n"
	if err := os.WriteFile(signaturePath, []byte(signature), 0o644); err != nil {
		t.Fatal(err)
	}
	activeKeyPath := filepath.Join(root, "2026-08-26.pub")
	revokedKeyPath := filepath.Join(root, "2026-08-25-revoked.pub")
	if err := os.WriteFile(activeKeyPath, []byte(base64.StdEncoding.EncodeToString(activePublic)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(revokedKeyPath, []byte(base64.StdEncoding.EncodeToString(revokedPublic)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	keysIndexPath := filepath.Join(root, "release-keys.json")
	keysIndex := []map[string]string{
		{"name": "2026-08-25-revoked.pub", "path": "release-keys/2026-08-25-revoked.pub", "type": "file"},
		{"name": "2026-08-26.pub", "path": "release-keys/2026-08-26.pub", "type": "file"},
	}
	writeJSON(t, keysIndexPath, keysIndex)

	releasePath := filepath.Join(root, "release.json")
	release := map[string]any{
		"tag_name": "v0.1",
		"assets": []map[string]string{
			{"browser_download_url": "http://127.0.0.1:1/downloads/" + archiveName},
			{"browser_download_url": "http://127.0.0.1:1/downloads/elgatolight-v0.1-checksums.txt"},
			{"browser_download_url": "http://127.0.0.1:1/downloads/elgatolight-v0.1-checksums.txt.sig"},
		},
	}
	writeJSON(t, releasePath, release)

	fakeBin := filepath.Join(root, "bin")
	if err := os.Mkdir(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fakeBin, "curl"), []byte(fakeCurl), 0o755); err != nil {
		t.Fatal(err)
	}
	return installerFixture{
		root: root, archive: archivePath, checksums: checksumsPath, signature: signaturePath,
		release: releasePath, keysIndex: keysIndexPath, activeKey: activeKeyPath, revokedKey: revokedKeyPath,
		fakeBin: fakeBin, installDir: filepath.Join(root, "installed"), archiveName: archiveName, expectedVersion: "v0.1",
	}
}

func (fixture installerFixture) run(t *testing.T, disableOpenSSL bool, args ...string) ([]byte, error) {
	t.Helper()
	if disableOpenSSL {
		if err := os.WriteFile(filepath.Join(fixture.fakeBin, "openssl"), []byte("#!/bin/sh\nexit 127\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	commandArgs := append([]string{filepath.Join(repositoryRoot(t), "install.sh")}, args...)
	command := exec.Command("sh", commandArgs...)
	noStable := "0"
	if fixture.noStable {
		noStable = "1"
	}
	environment := map[string]string{
		"PATH":                                fixture.fakeBin + string(os.PathListSeparator) + os.Getenv("PATH"),
		"FAKE_ACTIVE_KEY":                     fixture.activeKey,
		"FAKE_ARCHIVE":                        fixture.archive,
		"FAKE_CHECKSUMS":                      fixture.checksums,
		"FAKE_KEYS_INDEX":                     fixture.keysIndex,
		"FAKE_NO_STABLE":                      noStable,
		"FAKE_RELEASE":                        fixture.release,
		"FAKE_REVOKED_KEY":                    fixture.revokedKey,
		"FAKE_SIGNATURE":                      fixture.signature,
		"RELEASE_SIGNING_KEYS_API":            "http://127.0.0.1:1/central-key-api",
		"RELEASE_SIGNING_KEYS_REPOSITORY_URL": "http://127.0.0.1:1/central-key-repository",
	}
	environment["ELGATOLIGHT_ARCH"] = "x86_64"
	environment["ELGATOLIGHT_INSTALL_DIR"] = fixture.installDir
	environment["ELGATOLIGHT_OS"] = "Linux"
	environment["ELGATOLIGHT_RELEASE_API"] = "http://127.0.0.1:1/project-api"
	command.Env = testEnvironment(environment)
	return command.CombinedOutput()
}

func writeJSON(t *testing.T, path string, value any) {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, encoded, 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeArchive(t *testing.T, destination, version string) {
	t.Helper()
	archive, err := os.Create(destination)
	if err != nil {
		t.Fatal(err)
	}
	gzipWriter := gzip.NewWriter(archive)
	tarWriter := tar.NewWriter(gzipWriter)
	binary := []byte("#!/bin/sh\nprintf '%s\\n' '" + version + "'\n")
	header := &tar.Header{Name: "elgatolight-" + version + "-linux-amd64/elgatolight", Mode: 0o755, Size: int64(len(binary)), Typeflag: tar.TypeReg}
	if err := tarWriter.WriteHeader(header); err != nil {
		t.Fatal(err)
	}
	if _, err := tarWriter.Write(binary); err != nil {
		t.Fatal(err)
	}
	for _, closer := range []interface{ Close() error }{tarWriter, gzipWriter, archive} {
		if err := closer.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func testEnvironment(overrides map[string]string) []string {
	values := make(map[string]string)
	for _, value := range os.Environ() {
		key, content, found := strings.Cut(value, "=")
		if found {
			values[key] = content
		}
	}
	for key, value := range overrides {
		values[key] = value
	}
	result := make([]string, 0, len(values))
	for key, value := range values {
		result = append(result, key+"="+value)
	}
	sort.Strings(result)
	return result
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate repository root")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
}
