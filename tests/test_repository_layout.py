"""Static repository checks for the HACS custom-integration package."""

from __future__ import annotations

import json
from pathlib import Path


ROOT = Path(__file__).parents[1]


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


def test_unpublished_repository_placeholder_is_explicit() -> None:
    """RIP-311 has one searchable marker to replace after publication."""
    manifest = (ROOT / "custom_components/elgatolight/manifest.json").read_text(
        encoding="utf-8"
    )
    readme = (ROOT / "README.md").read_text(encoding="utf-8")
    assert "REPOSITORY_URL_PLACEHOLDER" in manifest
    assert "REPOSITORY_URL_PLACEHOLDER" in readme
    assert "RIP-311" in readme
