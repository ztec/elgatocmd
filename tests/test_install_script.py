from __future__ import annotations

import hashlib
import json
import os
from pathlib import Path
import subprocess
import tarfile

import pytest


ROOT = Path(__file__).resolve().parents[1]
INSTALLER = ROOT / "install.sh"


def _release_fixtures(tmp_path: Path, target: str) -> tuple[Path, Path, Path]:
    artifact_name = f"elgatolight-test-{target}.tar.gz"
    package = tmp_path / "package" / f"elgatolight-test-{target}"
    package.mkdir(parents=True)
    binary = package / "elgatolight"
    binary.write_text("#!/bin/sh\nprintf '%s\\n' test-version\n")
    binary.chmod(0o755)

    archive = tmp_path / artifact_name
    with tarfile.open(archive, "w:gz") as bundle:
        bundle.add(package, arcname=package.name)

    checksum = hashlib.sha256(archive.read_bytes()).hexdigest()
    checksums = tmp_path / "elgatolight-test-checksums.txt"
    checksums.write_text(f"{checksum}  {artifact_name}\n")

    release = tmp_path / "release.json"
    release.write_text(
        json.dumps(
            {
                "tag_name": "test-version",
                "prerelease": True,
                "assets": [
                    {"browser_download_url": f"https://downloads.test/{artifact_name}"},
                    {
                        "browser_download_url":
                            "https://downloads.test/elgatolight-test-checksums.txt"
                    },
                ],
            }
        )
    )
    return archive, checksums, release


def _fake_curl(tmp_path: Path) -> Path:
    fake_bin = tmp_path / "bin"
    fake_bin.mkdir()
    curl = fake_bin / "curl"
    curl.write_text(
        """#!/bin/sh
set -eu
url=
output=
while [ "$#" -gt 0 ]; do
    case "$1" in
        -o) output=$2; shift 2 ;;
        -*) shift ;;
        *) url=$1; shift ;;
    esac
done
case "$url" in
    */releases/latest)
        [ "${FAKE_STABLE_AVAILABLE:-0}" = 1 ] || exit 22
        cp "$FAKE_RELEASE" "$output"
        ;;
    *pre-release=true*) cp "$FAKE_RELEASE" "$output" ;;
    *-checksums.txt) cp "$FAKE_CHECKSUMS" "$output" ;;
    *.tar.gz) cp "$FAKE_ARCHIVE" "$output" ;;
    *) exit 22 ;;
esac
"""
    )
    curl.chmod(0o755)
    return fake_bin


@pytest.mark.parametrize(
    ("stable_available", "expected_channel"),
    [(True, "stable release"), (False, "pre-release")],
)
def test_installer_selects_stable_then_falls_back_to_prerelease(
    tmp_path: Path, stable_available: bool, expected_channel: str
) -> None:
    archive, checksums, release = _release_fixtures(tmp_path, "linux-amd64")
    fake_bin = _fake_curl(tmp_path)
    install_dir = tmp_path / "installed"
    env = os.environ | {
        "PATH": f"{fake_bin}:{os.environ['PATH']}",
        "ELGATOLIGHT_OS": "Linux",
        "ELGATOLIGHT_ARCH": "x86_64",
        "ELGATOLIGHT_INSTALL_DIR": str(install_dir),
        "ELGATOLIGHT_RELEASE_API": "https://api.test/repos/ztec/elgatocmd",
        "FAKE_STABLE_AVAILABLE": "1" if stable_available else "0",
        "FAKE_ARCHIVE": str(archive),
        "FAKE_CHECKSUMS": str(checksums),
        "FAKE_RELEASE": str(release),
    }

    result = subprocess.run(
        ["sh", str(INSTALLER)],
        env=env,
        stdin=subprocess.DEVNULL,
        capture_output=True,
        text=True,
        check=True,
    )

    installed = install_dir / "elgatolight"
    assert installed.is_file()
    assert os.access(installed, os.X_OK)
    assert expected_channel in result.stdout
    assert str(installed) in result.stdout


def test_installer_rejects_an_unsupported_architecture() -> None:
    result = subprocess.run(
        ["sh", str(INSTALLER)],
        env=os.environ
        | {
            "ELGATOLIGHT_OS": "Linux",
            "ELGATOLIGHT_ARCH": "mips64",
            "ELGATOLIGHT_INSTALL_DIR": "/unused",
        },
        stdin=subprocess.DEVNULL,
        capture_output=True,
        text=True,
    )

    assert result.returncode != 0
    assert "unsupported architecture: mips64" in result.stderr


def test_installer_and_documented_one_liners_are_well_formed() -> None:
    subprocess.run(["sh", "-n", str(INSTALLER)], check=True)
    readme = (ROOT / "README.md").read_text()
    assert "git2.riper.fr/ztec/elgatocmd/raw/branch/main/install.sh | sh" in readme
    assert "github.com/ztec/elgatocmd/raw/refs/heads/main/install.sh | sh" in readme
    assert readme.count("curl -fsSL") >= 2
    assert readme.count("wget -qO-") >= 2
