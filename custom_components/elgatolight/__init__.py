"""Elgato USB Light Bridge integration."""

from __future__ import annotations

from homeassistant.config_entries import ConfigEntry
from homeassistant.const import Platform
from homeassistant.core import HomeAssistant

from .bridge import BridgeHub, async_register_websocket_api
from .const import DOMAIN

PLATFORMS = [Platform.LIGHT, Platform.SCENE]


async def async_setup(hass: HomeAssistant, config: dict) -> bool:
    """Register the WebSocket commands used by outbound daemons."""
    async_register_websocket_api(hass)
    return True


async def async_setup_entry(hass: HomeAssistant, entry: ConfigEntry) -> bool:
    """Set up the singleton bridge config entry."""
    hub = BridgeHub(hass)
    await hub.async_load()
    hass.data.setdefault(DOMAIN, {})[entry.entry_id] = hub
    entry.runtime_data = hub
    await hass.config_entries.async_forward_entry_setups(entry, PLATFORMS)
    return True


async def async_unload_entry(hass: HomeAssistant, entry: ConfigEntry) -> bool:
    """Unload the bridge and all light entities."""
    if not await hass.config_entries.async_unload_platforms(entry, PLATFORMS):
        return False
    hub: BridgeHub = entry.runtime_data
    await hub.async_shutdown()
    hass.data[DOMAIN].pop(entry.entry_id, None)
    return True
