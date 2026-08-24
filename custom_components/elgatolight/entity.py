"""Shared Home Assistant entity helpers for Elgato USB lights."""

from __future__ import annotations

from homeassistant.helpers.device_registry import DeviceInfo

from .bridge import BridgeHub
from .const import DOMAIN


def bridge_device_info(hub: BridgeHub, device_id: str) -> DeviceInfo:
    """Return stable registry metadata shared by every device entity."""
    device = hub.device(device_id) or {}
    return DeviceInfo(
        identifiers={(DOMAIN, device_id)},
        manufacturer=device.get("manufacturer", "Elgato"),
        model=device.get("model", "Key Light Neo"),
        name=device.get("name", "Elgato Key Light Neo"),
        sw_version=device.get("firmware") or None,
    )
