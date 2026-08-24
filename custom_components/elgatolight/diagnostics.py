"""Diagnostics for the Elgato USB Light Bridge integration."""

from __future__ import annotations

from typing import Any

from homeassistant.config_entries import ConfigEntry
from homeassistant.core import HomeAssistant

from .bridge import BridgeHub


async def async_get_config_entry_diagnostics(
    hass: HomeAssistant, entry: ConfigEntry
) -> dict[str, Any]:
    """Return bridge metadata; daemon OAuth credentials never enter HA storage."""
    hub: BridgeHub = entry.runtime_data
    return hub.diagnostics()
