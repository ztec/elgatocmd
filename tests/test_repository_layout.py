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
