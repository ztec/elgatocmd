"""Static repository checks for the HACS custom-integration package."""

from __future__ import annotations

import json
from pathlib import Path


ROOT = Path(__file__).parents[1]
REPOSITORY_URL = "https://github.com/ztec/elgatocmd"


def test_hacs_layout_and_manifest() -> None:
    """Keep the mixed Go/Python repository installable by HACS."""
    component_root = ROOT / "custom_components"
    assert [path.name for path in component_root.iterdir() if path.is_dir()] == [
        "elgatolight"
    ]
    hacs = json.loads((ROOT / "hacs.json").read_text(encoding="utf-8"))
    assert hacs["name"] == "Elgato USB Light Bridge"

    manifest = json.loads(
        (component_root / "elgatolight" / "manifest.json").read_text(encoding="utf-8")
    )
    assert manifest["domain"] == "elgatolight"
    assert manifest["config_flow"] is True
    assert manifest["single_config_entry"] is True
    assert manifest["iot_class"] == "local_push"
    assert manifest["integration_type"] == "hub"
    assert manifest["version"] == "0.1.0"
    for key in ("name", "codeowners", "documentation", "issue_tracker"):
        assert manifest[key]


def test_published_repository_metadata() -> None:
    """Keep repository links and ownership tied to the canonical remote."""
    manifest_text = (
        ROOT / "custom_components/elgatolight/manifest.json"
    ).read_text(encoding="utf-8")
    manifest = json.loads(manifest_text)
    readme = (ROOT / "README.md").read_text(encoding="utf-8")
    technical_docs = (ROOT / "docs/technical.md").read_text(encoding="utf-8")
    published_metadata = "\n".join((manifest_text, readme, technical_docs))
    assert manifest["codeowners"] == ["@ztec"]
    assert manifest["documentation"] == REPOSITORY_URL
    assert manifest["issue_tracker"] == f"{REPOSITORY_URL}/issues"
    assert REPOSITORY_URL in readme
    assert "[Technical documentation](docs/technical.md)" in readme
    assert len(readme.splitlines()) < 120
    assert "PLACEHOLDER" not in published_metadata


def test_forgejo_workflows_use_the_project_runner_and_container_build() -> None:
    """Keep CI and releases reproducible through the Dockerfile image."""
    test_workflow = (ROOT / ".forgejo/workflows/test.yaml").read_text(
        encoding="utf-8"
    )
    release_workflow = (ROOT / ".forgejo/workflows/release.yaml").read_text(
        encoding="utf-8"
    )

    assert "runs-on: ubuntu-24.04" in test_workflow
    assert "make container-test CONTAINER_ENGINE=docker" in test_workflow
    assert 'branches:\n      - "**"' in test_workflow
    assert "pull_request" not in test_workflow
    assert "workflow_dispatch" not in test_workflow
    assert "runs-on: ubuntu-24.04" in release_workflow
    assert 'tags:\n      - "**"' in release_workflow
    assert "release tag is required" in release_workflow
    assert "^[0-9]+\\.[0-9]+$" not in release_workflow
    assert "printf '%s' \"${version}\" >.elgatolight-release-version" in release_workflow
    assert "VERSION_FILE=.elgatolight-release-version" in release_workflow
    assert "make release CONTAINER_ENGINE=docker" in release_workflow
    assert "https://code.forgejo.org/actions/forgejo-release@v2.13.4" in release_workflow
    assert "release-dir: dist" in release_workflow
    assert "override: true" in release_workflow
    assert "prerelease: true" in release_workflow
    assert "hide-archive-link: true" in release_workflow
    assert 'git rev-parse --verify "refs/tags/${version}^{commit}"' in release_workflow
    assert "sha: ${{ steps.version.outputs.sha }}" in release_workflow


def test_distrobox_and_automation_share_the_dockerfile_image() -> None:
    """Avoid a second, drifting dependency definition for development."""
    dockerfile = (ROOT / "Dockerfile").read_text(encoding="utf-8")
    distrobox = (ROOT / "distrobox.ini").read_text(encoding="utf-8")
    makefile = (ROOT / "Makefile").read_text(encoding="utf-8")

    assert "golang" in dockerfile
    assert "requirements-dev.txt" in dockerfile
    assert "COPY . ." in dockerfile
    assert "image=localhost/elgatolight-build:dev" in distrobox
    assert "CONTAINER_ENGINE ?=" in makefile
    assert "DBX_CONTAINER_MANAGER=$(CONTAINER_MANAGER_NAME)" in makefile
    assert "container-test:" in makefile
    assert '--volume "$(CURDIR):/workspace:Z"' not in makefile
    assert 'cp "$$container_id:/tmp/elgatolight-release/."' in makefile
    assert "setup:" not in makefile


def test_release_targets_only_publish_supported_distribution_formats() -> None:
    release_script = (ROOT / "scripts/build-release.sh").read_text(encoding="utf-8")
    installer = (ROOT / "install.sh").read_text(encoding="utf-8")
    assert "build_target linux amd64" in release_script
    assert "build_target linux arm64" in release_script
    assert "build_target linux arm 7" in release_script
    assert "build_target darwin amd64" in release_script
    assert "build_target darwin arm64" in release_script
    assert "build_target windows" not in release_script
    assert "MINGW" not in installer
    assert "windows" not in installer.lower()


def test_installer_explains_privilege_aware_setup_modes() -> None:
    installer = (ROOT / "install.sh").read_text(encoding="utf-8")
    readme = (ROOT / "README.md").read_text(encoding="utf-8")
    technical = (ROOT / "docs/technical.md").read_text(encoding="utf-8")
    assert "User service: starts when you log in. Only the USB rule uses sudo." in installer
    assert "System service: starts at boot and runs as root." in installer
    assert "setup --scope none" in installer
    assert "`elgatolight setup`: user service" in readme
    assert "`sudo elgatolight setup`: system service" in readme
    assert "--target-user" not in readme
    assert "--target-user" not in technical
