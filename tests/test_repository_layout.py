"""Static repository checks for the HACS package and product-specific tooling."""

from __future__ import annotations

import json
import struct
from pathlib import Path


ROOT = Path(__file__).parents[1]
GITHUB_REPOSITORY = "https://github.com/ztec/elgatocmd"
FORGEJO_REPOSITORY = "https://git2.riper.fr/ztec/elgatocmd"


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
    assert manifest["version"] == "0.2.2"
    for key in ("name", "codeowners", "documentation", "issue_tracker"):
        assert manifest[key]

    icon = (component_root / "elgatolight" / "brand" / "icon.png").read_bytes()
    assert icon[:8] == b"\x89PNG\r\n\x1a\n"
    width, height, _depth, color_type, _compression, _filter, _interlace = (
        struct.unpack(">IIBBBBB", icon[16:29])
    )
    assert width == height == 256
    assert color_type in (4, 6)

    light_platform = (component_root / "elgatolight" / "light.py").read_text(
        encoding="utf-8"
    )
    assert '_attr_icon = "mdi:television-ambient-light"' in light_platform
    scene_platform = (component_root / "elgatolight" / "scene.py").read_text(
        encoding="utf-8"
    )
    assert 'self._attr_icon = f"mdi:numeric-{preset}-box"' in scene_platform

    integration = (component_root / "elgatolight" / "__init__.py").read_text(
        encoding="utf-8"
    )
    assert "Platform.SCENE" in integration
    assert "Platform.BUTTON" not in integration
    assert not (component_root / "elgatolight" / "button.py").exists()

    expected_preset_entities = {
        "scene": {
            "preset_1": {"name": "I"},
            "preset_2": {"name": "II"},
        }
    }
    for translation in ("strings.json", "translations/en.json"):
        strings = json.loads(
            (component_root / "elgatolight" / translation).read_text(encoding="utf-8")
        )
        assert strings["entity"] == expected_preset_entities


def test_published_repository_metadata_and_license() -> None:
    """Keep public metadata tied to the canonical project and GPL license."""
    manifest_text = (
        ROOT / "custom_components/elgatolight/manifest.json"
    ).read_text(encoding="utf-8")
    manifest = json.loads(manifest_text)
    readme = (ROOT / "README.md").read_text(encoding="utf-8")
    technical_docs = (ROOT / "docs/technical.md").read_text(encoding="utf-8")
    license_text = (ROOT / "LICENSE").read_text(encoding="utf-8")
    published_metadata = "\n".join((manifest_text, readme, technical_docs))
    assert manifest["codeowners"] == ["@ztec"]
    assert manifest["documentation"] == GITHUB_REPOSITORY
    assert manifest["issue_tracker"] == f"{GITHUB_REPOSITORY}/issues"
    assert GITHUB_REPOSITORY in readme
    assert FORGEJO_REPOSITORY in readme
    assert "[Technical documentation](docs/technical.md)" in readme
    assert "[GNU General Public License v3.0 only](LICENSE)" in readme
    assert "GNU GENERAL PUBLIC LICENSE" in license_text
    assert "Version 3, 29 June 2007" in license_text
    assert len(readme.splitlines()) < 120
    assert "PLACEHOLDER" not in published_metadata


def test_template_automation_runs_the_product_suites() -> None:
    """Keep template-owned CI connected to Go and Home Assistant behavior."""
    test_workflow = (ROOT / ".forgejo/workflows/test.yaml").read_text(
        encoding="utf-8"
    )
    release_workflow = (ROOT / ".forgejo/workflows/test-and-release.yaml").read_text(
        encoding="utf-8"
    )
    dockerfile = (ROOT / "Dockerfile").read_text(encoding="utf-8")
    makefile = (ROOT / "Makefile").read_text(encoding="utf-8")
    distrobox = (ROOT / "distrobox.ini").read_text(encoding="utf-8")

    assert "runs-on: ubuntu-24.04" in test_workflow
    assert "make ci CONTAINER_ENGINE=docker" in test_workflow
    assert "runs-on: ubuntu-24.04" in release_workflow
    assert "Build and sign every release target" in release_workflow
    assert "requirements-dev.txt" in dockerfile
    assert "/opt/elgatolight-venv" in dockerfile
    assert "python-test:" in makefile
    assert "$(PYTHON) -m pytest -q tests" in makefile
    assert "test-native:" in makefile and "python-test" in makefile
    assert "image=localhost/elgatolight-dev:local" in distrobox


def test_release_and_install_keep_linux_product_policy() -> None:
    """Retain Linux targets, signed updates, and setup guidance."""
    release_script = (ROOT / "scripts/build-release.sh").read_text(encoding="utf-8")
    installer = (ROOT / "install.sh").read_text(encoding="utf-8")
    readme = (ROOT / "README.md").read_text(encoding="utf-8")
    technical = (ROOT / "docs/technical.md").read_text(encoding="utf-8")

    for target in (
        "build_target linux amd64",
        "build_target linux arm64",
        "build_target linux arm 7",
    ):
        assert target in release_script
    assert "build_target darwin" not in release_script.lower()
    assert "build_target windows" not in release_script
    assert "darwin" not in installer.lower()
    assert "windows" not in installer.lower()
    assert "checksums.txt.sig" in installer
    assert "release-keys?ref=main" in installer
    assert "--skip-signature-verification" in installer
    assert "User service: starts when you log in. Only the USB rule uses sudo." in installer
    assert "System service: starts at boot and runs as root." in installer
    assert "setup --scope none" in installer
    assert "`elgatolight setup`: user service" in readme
    assert "`sudo elgatolight setup`: system service" in readme
    assert "| sh" not in readme
    assert "--target-user" not in readme
    assert "--target-user" not in technical
