"""Native Home Assistant light entities backed by an outbound daemon."""

from __future__ import annotations

from typing import Any

from homeassistant.components.light import (
    ATTR_BRIGHTNESS,
    ATTR_COLOR_TEMP_KELVIN,
    ColorMode,
    LightEntity,
)
from homeassistant.config_entries import ConfigEntry
from homeassistant.core import HomeAssistant, callback
from homeassistant.helpers.device_registry import DeviceInfo
from homeassistant.helpers.entity_platform import AddConfigEntryEntitiesCallback

from .bridge import BridgeHub
from .entity import bridge_device_info


async def async_setup_entry(
    hass: HomeAssistant,
    entry: ConfigEntry,
    async_add_entities: AddConfigEntryEntitiesCallback,
) -> None:
    """Create retained entities and subscribe to new daemon devices."""
    hub: BridgeHub = entry.runtime_data
    added: set[str] = set()

    @callback
    def add_device(device_id: str) -> None:
        if device_id in added:
            return
        added.add(device_id)
        async_add_entities([ElgatoBridgeLight(hub, device_id)])

    for device_id in hub.device_ids():
        add_device(device_id)
    entry.async_on_unload(hub.subscribe_new_devices(add_device))


class ElgatoBridgeLight(LightEntity):
    """One USB light announced by an elgatolight daemon."""

    _attr_color_mode = ColorMode.COLOR_TEMP
    _attr_has_entity_name = True
    _attr_icon = "mdi:television-ambient-light"
    _attr_name = None
    _attr_should_poll = False
    _attr_supported_color_modes = {ColorMode.COLOR_TEMP}

    def __init__(self, hub: BridgeHub, device_id: str) -> None:
        self._hub = hub
        self._device_id = device_id
        self._attr_unique_id = device_id
        self._unsubscribe = None
        self._apply_model()

    @property
    def device_info(self) -> DeviceInfo:
        """Return stable device-registry metadata."""
        return bridge_device_info(self._hub, self._device_id)

    async def async_added_to_hass(self) -> None:
        """Subscribe to push state for this light."""
        await super().async_added_to_hass()
        self._unsubscribe = self._hub.subscribe_device(
            self._device_id, self._handle_model_update
        )

    async def async_will_remove_from_hass(self) -> None:
        """Release the push subscription."""
        if self._unsubscribe is not None:
            self._unsubscribe()
            self._unsubscribe = None
        await super().async_will_remove_from_hass()

    async def async_turn_on(self, **kwargs: Any) -> None:
        """Send one atomic partial update to the daemon."""
        update: dict[str, Any] = {"on": True}
        if ATTR_BRIGHTNESS in kwargs:
            update["brightness"] = self._ha_to_device_brightness(
                int(kwargs[ATTR_BRIGHTNESS])
            )
        if ATTR_COLOR_TEMP_KELVIN in kwargs:
            update["temperature"] = int(kwargs[ATTR_COLOR_TEMP_KELVIN])
        await self._hub.async_command(self._device_id, update)

    async def async_turn_off(self, **kwargs: Any) -> None:
        """Turn the physical light off."""
        await self._hub.async_command(self._device_id, {"on": False})

    @callback
    def _handle_model_update(self) -> None:
        self._apply_model()
        self.async_write_ha_state()

    def _apply_model(self) -> None:
        device = self._hub.device(self._device_id)
        if device is None:
            self._attr_available = False
            return
        state = device["state"]
        capabilities = device["capabilities"]
        self._attr_available = bool(device["available"])
        self._attr_is_on = bool(state["on"])
        self._attr_brightness = self._device_to_ha_brightness(int(state["brightness"]))
        self._attr_color_temp_kelvin = int(state["temperature"])
        self._attr_min_color_temp_kelvin = int(capabilities["minKelvin"])
        self._attr_max_color_temp_kelvin = int(capabilities["maxKelvin"])

    def _device_to_ha_brightness(self, value: int) -> int:
        device = self._hub.device(self._device_id)
        maximum = int(device["capabilities"]["maxBrightness"]) if device else 100
        return max(0, min(255, round(value * 255 / maximum)))

    def _ha_to_device_brightness(self, value: int) -> int:
        device = self._hub.device(self._device_id)
        maximum = int(device["capabilities"]["maxBrightness"]) if device else 100
        return max(0, min(maximum, round(value * maximum / 255)))
