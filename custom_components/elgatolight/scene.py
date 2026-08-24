"""Preset scenes backed by slots stored on each physical light."""

from __future__ import annotations

from typing import Any

from homeassistant.components.scene import Scene
from homeassistant.config_entries import ConfigEntry
from homeassistant.core import Context, HomeAssistant, callback
from homeassistant.helpers.device_registry import DeviceInfo
from homeassistant.helpers.entity_platform import AddConfigEntryEntitiesCallback

from .bridge import BridgeHub
from .entity import bridge_device_info


async def async_setup_entry(
    hass: HomeAssistant,
    entry: ConfigEntry,
    async_add_entities: AddConfigEntryEntitiesCallback,
) -> None:
    """Create two preset scenes for each retained light."""
    hub: BridgeHub = entry.runtime_data
    added: set[str] = set()

    @callback
    def add_device(device_id: str) -> None:
        if device_id in added:
            return
        added.add(device_id)
        async_add_entities(
            [ElgatoPresetScene(hub, device_id, preset) for preset in (1, 2)]
        )

    for device_id in hub.device_ids():
        add_device(device_id)
    entry.async_on_unload(hub.subscribe_new_devices(add_device))


class ElgatoPresetScene(Scene):
    """A stateless Home Assistant scene that recalls one hardware preset."""

    _attr_has_entity_name = True
    _attr_should_poll = False

    def __init__(self, hub: BridgeHub, device_id: str, preset: int) -> None:
        self._hub = hub
        self._device_id = device_id
        self._preset = preset
        self._unsubscribe = None
        self._attr_unique_id = f"{device_id}_preset_{preset}_scene"
        self._attr_translation_key = f"preset_{preset}"
        self._attr_icon = f"mdi:numeric-{preset}-box"
        self._apply_model()

    @property
    def device_info(self) -> DeviceInfo:
        """Attach the scene to the physical light device."""
        return bridge_device_info(self._hub, self._device_id)

    async def async_added_to_hass(self) -> None:
        """Subscribe to device availability updates."""
        await super().async_added_to_hass()
        self._unsubscribe = self._hub.subscribe_device(
            self._device_id, self._handle_model_update
        )

    async def async_will_remove_from_hass(self) -> None:
        """Release the device subscription."""
        if self._unsubscribe is not None:
            self._unsubscribe()
            self._unsubscribe = None
        await super().async_will_remove_from_hass()

    async def async_activate(self, **kwargs: Any) -> None:
        """Recall the current contents of this preset slot."""
        await self._hub.async_apply_preset(
            self._device_id, self._preset, context=self._context
        )

    @callback
    def _handle_model_update(self, context: Context) -> None:
        self.async_set_context(context)
        self._apply_model()
        self.async_write_ha_state()

    def _apply_model(self) -> None:
        device = self._hub.device(self._device_id)
        self._attr_available = bool(device and device["available"])
