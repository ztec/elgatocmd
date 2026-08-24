"""Outbound daemon session registry and Home Assistant WebSocket protocol."""

from __future__ import annotations

import asyncio
from collections.abc import Callable
from dataclasses import dataclass, field
import logging
from typing import Any
import uuid

import voluptuous as vol

from homeassistant.components import websocket_api
from homeassistant.core import HomeAssistant, callback
from homeassistant.exceptions import HomeAssistantError
from homeassistant.helpers.storage import Store

from .const import (
    COMMAND_TIMEOUT,
    DOMAIN,
    INTEGRATION_NAME,
    PROTOCOL_VERSION,
    STORAGE_KEY,
    STORAGE_VERSION,
    WS_COMMAND_RESULT,
    WS_DEVICE_CONNECTED,
    WS_DEVICE_DISCONNECTED,
    WS_INFO,
    WS_STATE,
    WS_SUBSCRIBE,
)

_LOGGER = logging.getLogger(__name__)


@dataclass(slots=True)
class BridgeSession:
    """One live outbound daemon connection."""

    instance_id: str
    epoch: str
    connection: websocket_api.ActiveConnection
    subscription_id: int
    last_sequence: int
    active: bool = True
    pending: dict[str, tuple[str, asyncio.Future[dict[str, Any]]]] = field(
        default_factory=dict
    )


class BridgeHub:
    """Own daemon sessions and the persistent device inventory."""

    def __init__(self, hass: HomeAssistant) -> None:
        self.hass = hass
        self._store: Store[dict[str, Any]] = Store(hass, STORAGE_VERSION, STORAGE_KEY)
        self._devices: dict[str, dict[str, Any]] = {}
        self._sessions: dict[str, BridgeSession] = {}
        self._device_listeners: dict[str, set[Callable[[], None]]] = {}
        self._new_device_listeners: set[Callable[[str], None]] = set()

    async def async_load(self) -> None:
        """Load known entities before any daemon reconnects."""
        stored = await self._store.async_load() or {}
        for device_id, payload in stored.get("devices", {}).items():
            device = _normalize_device(payload, payload.get("instanceId", ""))
            device["available"] = False
            self._devices[device_id] = device

    async def async_shutdown(self) -> None:
        """Fail pending commands and detach all sessions."""
        for session in tuple(self._sessions.values()):
            self._close_session(session, "Home Assistant integration unloaded")
        self._sessions.clear()

    def device_ids(self) -> list[str]:
        """Return all persistent device IDs."""
        return sorted(self._devices)

    def device(self, device_id: str) -> dict[str, Any] | None:
        """Return the current device model."""
        return self._devices.get(device_id)

    def diagnostics(self) -> dict[str, Any]:
        """Return a JSON-safe bridge snapshot without OAuth credentials."""
        return {
            "protocolVersion": PROTOCOL_VERSION,
            "sessions": [
                {
                    "instanceId": session.instance_id,
                    "epoch": session.epoch,
                    "lastSequence": session.last_sequence,
                    "active": session.active,
                    "pendingCommands": len(session.pending),
                }
                for session in self._sessions.values()
            ],
            "devices": [dict(device) for device in self._devices.values()],
        }

    @callback
    def subscribe_device(self, device_id: str, listener: Callable[[], None]) -> Callable[[], None]:
        """Subscribe an entity to one device model."""
        listeners = self._device_listeners.setdefault(device_id, set())
        listeners.add(listener)

        @callback
        def unsubscribe() -> None:
            listeners.discard(listener)

        return unsubscribe

    @callback
    def subscribe_new_devices(self, listener: Callable[[str], None]) -> Callable[[], None]:
        """Subscribe the light platform to dynamic inventory additions."""
        self._new_device_listeners.add(listener)

        @callback
        def unsubscribe() -> None:
            self._new_device_listeners.discard(listener)

        return unsubscribe

    async def async_register(
        self,
        connection: websocket_api.ActiveConnection,
        subscription_id: int,
        msg: dict[str, Any],
    ) -> BridgeSession:
        """Register or replace one daemon and apply its complete snapshot."""
        if msg["protocolVersion"] != PROTOCOL_VERSION:
            raise HomeAssistantError(
                f"Unsupported bridge protocol {msg['protocolVersion']}; expected {PROTOCOL_VERSION}"
            )
        instance_id = msg["instanceId"].strip()
        epoch = msg["epoch"].strip()
        if not instance_id or not epoch:
            raise HomeAssistantError("instanceId and epoch must not be empty")

        normalized_devices: list[dict[str, Any]] = []
        seen: set[str] = set()
        for payload in msg["devices"]:
            device = _normalize_device(payload, instance_id)
            device_id = device["id"]
            if device_id in seen:
                raise HomeAssistantError(f"duplicate device id {device_id}")
            seen.add(device_id)
            normalized_devices.append(device)

        previous = self._sessions.get(instance_id)
        if previous is not None:
            self._close_session(previous, "Replaced by a newer daemon connection")

        session = BridgeSession(
            instance_id=instance_id,
            epoch=epoch,
            connection=connection,
            subscription_id=subscription_id,
            last_sequence=msg["sequence"],
        )
        self._sessions[instance_id] = session

        for device in normalized_devices:
            self._put_device(device)
        for device_id, device in tuple(self._devices.items()):
            if device.get("instanceId") == instance_id and device_id not in seen:
                unavailable = dict(device)
                unavailable["available"] = False
                unavailable["error"] = "not present in daemon snapshot"
                self._put_device(unavailable)
        await self._async_save()
        _LOGGER.info("Elgato daemon %s connected with %d light(s)", instance_id, len(seen))
        return session

    async def async_disconnect(self, session: BridgeSession) -> None:
        """Mark one daemon's entities unavailable when its socket closes."""
        if self._sessions.get(session.instance_id) is not session:
            return
        self._sessions.pop(session.instance_id, None)
        self._close_session(session, "Daemon connection closed")
        for device_id, device in tuple(self._devices.items()):
            if device.get("instanceId") != session.instance_id:
                continue
            unavailable = dict(device)
            unavailable["available"] = False
            unavailable["error"] = "daemon disconnected"
            self._put_device(unavailable)
        await self._async_save()
        _LOGGER.info("Elgato daemon %s disconnected", session.instance_id)

    async def async_apply_state(
        self,
        connection: websocket_api.ActiveConnection,
        msg: dict[str, Any],
    ) -> None:
        """Apply one sequenced authoritative daemon event."""
        session = self._require_session(connection, msg)
        sequence = msg["sequence"]
        if sequence != session.last_sequence + 1:
            session.connection.send_event(
                session.subscription_id,
                {
                    "event": "resync",
                    "reason": f"expected sequence {session.last_sequence + 1}, got {sequence}",
                },
            )
            raise HomeAssistantError("bridge event sequence gap; full resync requested")
        event_type = msg["type"]
        if event_type in (WS_STATE, WS_DEVICE_CONNECTED):
            device = _normalize_device(msg["light"], session.instance_id)
        elif event_type == WS_DEVICE_DISCONNECTED:
            device_id = msg["deviceId"]
            device = None
        else:
            raise HomeAssistantError(f"unsupported bridge event {event_type}")

        session.last_sequence = sequence
        if device is not None:
            self._put_device(device)
        else:
            device = self._devices.get(device_id)
            if device is not None and device.get("instanceId") == session.instance_id:
                unavailable = dict(device)
                unavailable["available"] = False
                unavailable["error"] = msg.get("error", "device disconnected")
                self._put_device(unavailable)
        await self._async_save()

    async def async_command_result(
        self,
        connection: websocket_api.ActiveConnection,
        msg: dict[str, Any],
    ) -> None:
        """Resolve a Home Assistant light action from the daemon response."""
        session = self._require_session(connection, msg)
        request_id = msg["requestId"]
        pending = session.pending.pop(request_id, None)
        if pending is None:
            raise HomeAssistantError(f"unknown or expired command result {request_id}")
        expected_device_id, future = pending
        if future.done():
            raise HomeAssistantError(f"unknown or expired command result {request_id}")
        if msg.get("light") is not None:
            device = _normalize_device(msg["light"], session.instance_id)
            if device["id"] != expected_device_id:
                err = HomeAssistantError(
                    f"command result device {device['id']} does not match {expected_device_id}"
                )
                future.set_exception(err)
                raise err
            self._put_device(device)
            await self._async_save()
        elif msg["success"]:
            err = HomeAssistantError("successful light command omitted authoritative state")
            future.set_exception(err)
            raise err
        if msg["success"]:
            future.set_result(msg)
        else:
            future.set_exception(HomeAssistantError(msg.get("error") or "light command failed"))

    async def async_command(self, device_id: str, update: dict[str, Any]) -> None:
        """Send a light action over the daemon-initiated subscription."""
        device = self._devices.get(device_id)
        if device is None:
            raise HomeAssistantError(f"Unknown Elgato light {device_id}")
        session = self._sessions.get(device.get("instanceId", ""))
        if session is None or not session.active:
            raise HomeAssistantError("Elgato daemon is unavailable")

        request_id = uuid.uuid4().hex
        future: asyncio.Future[dict[str, Any]] = self.hass.loop.create_future()
        session.pending[request_id] = (device_id, future)
        session.connection.send_event(
            session.subscription_id,
            {
                "event": "command",
                "requestId": request_id,
                "deviceId": device_id,
                "update": update,
            },
        )
        try:
            await asyncio.wait_for(future, timeout=COMMAND_TIMEOUT)
        except TimeoutError as err:
            session.pending.pop(request_id, None)
            raise HomeAssistantError("Timed out waiting for the Elgato daemon") from err

    def _require_session(
        self, connection: websocket_api.ActiveConnection, msg: dict[str, Any]
    ) -> BridgeSession:
        instance_id = msg["instanceId"]
        session = self._sessions.get(instance_id)
        if (
            session is None
            or not session.active
            or session.connection is not connection
            or session.epoch != msg["epoch"]
        ):
            raise HomeAssistantError("stale or unregistered daemon session")
        return session

    def _put_device(self, device: dict[str, Any]) -> None:
        device_id = device["id"]
        is_new = device_id not in self._devices
        self._devices[device_id] = device
        for listener in tuple(self._device_listeners.get(device_id, ())):
            listener()
        if is_new:
            for listener in tuple(self._new_device_listeners):
                listener(device_id)

    def _close_session(self, session: BridgeSession, reason: str) -> None:
        session.active = False
        for _, future in tuple(session.pending.values()):
            if not future.done():
                future.set_exception(HomeAssistantError(reason))
        session.pending.clear()

    async def _async_save(self) -> None:
        await self._store.async_save({"devices": self._devices})


def _normalize_device(payload: dict[str, Any], instance_id: str) -> dict[str, Any]:
    """Validate and copy an untrusted daemon device payload."""
    device_id = str(payload.get("id", "")).strip()
    if not device_id:
        raise HomeAssistantError("device id must not be empty")
    state = payload.get("state") or {}
    capabilities = payload.get("capabilities") or {}
    min_brightness = int(capabilities.get("minBrightness", 0))
    max_brightness = int(capabilities.get("maxBrightness", 100))
    min_kelvin = int(capabilities.get("minKelvin", 2900))
    max_kelvin = int(capabilities.get("maxKelvin", 7000))
    brightness = int(state.get("brightness", 0))
    temperature = int(state.get("temperature", min_kelvin))
    if max_brightness <= min_brightness or not min_brightness <= brightness <= max_brightness:
        raise HomeAssistantError(f"invalid brightness capability/state for {device_id}")
    if max_kelvin <= min_kelvin or not min_kelvin <= temperature <= max_kelvin:
        raise HomeAssistantError(f"invalid color temperature capability/state for {device_id}")
    return {
        "instanceId": instance_id or str(payload.get("instanceId", "")),
        "id": device_id,
        "stableId": bool(payload.get("stableId", False)),
        "name": str(payload.get("name") or "Elgato Key Light Neo"),
        "manufacturer": str(payload.get("manufacturer") or "Elgato"),
        "model": str(payload.get("model") or "Key Light Neo"),
        "firmware": str(payload.get("firmware") or ""),
        "available": bool(payload.get("available", False)),
        "state": {
            "on": bool(state.get("on", False)),
            "brightness": brightness,
            "temperature": temperature,
        },
        "capabilities": {
            "minBrightness": min_brightness,
            "maxBrightness": max_brightness,
            "minKelvin": min_kelvin,
            "maxKelvin": max_kelvin,
        },
        "error": str(payload.get("error") or ""),
    }


def _hub(hass: HomeAssistant) -> BridgeHub:
    entries = hass.data.get(DOMAIN, {})
    if not entries:
        raise HomeAssistantError("Elgato USB Light Bridge is not enabled")
    return next(iter(entries.values()))


def _send_handler_error(
    connection: websocket_api.ActiveConnection, msg: dict[str, Any], err: Exception
) -> None:
    connection.send_error(msg["id"], "invalid_request", str(err))


@websocket_api.websocket_command({vol.Required("type"): WS_INFO})
@websocket_api.require_admin
@callback
def websocket_info(
    hass: HomeAssistant,
    connection: websocket_api.ActiveConnection,
    msg: dict[str, Any],
) -> None:
    """Report the installed receiver and bridge version."""
    connection.send_result(
        msg["id"],
        {"name": INTEGRATION_NAME, "protocolVersion": PROTOCOL_VERSION},
    )


@websocket_api.websocket_command(
    {
        vol.Required("type"): WS_SUBSCRIBE,
        vol.Required("protocolVersion"): int,
        vol.Required("instanceId"): str,
        vol.Required("epoch"): str,
        vol.Required("sequence"): vol.All(int, vol.Range(min=0)),
        vol.Required("daemonVersion"): str,
        vol.Required("devices"): [dict],
    }
)
@websocket_api.require_admin
@websocket_api.async_response
async def websocket_subscribe(
    hass: HomeAssistant,
    connection: websocket_api.ActiveConnection,
    msg: dict[str, Any],
) -> None:
    """Register a daemon and retain the command subscription."""
    try:
        hub = _hub(hass)
        session = await hub.async_register(connection, msg["id"], msg)
    except (HomeAssistantError, ValueError, TypeError) as err:
        _send_handler_error(connection, msg, err)
        return

    @callback
    def unsubscribe() -> None:
        hass.async_create_task(hub.async_disconnect(session))

    connection.subscriptions[msg["id"]] = unsubscribe
    connection.send_result(msg["id"], {"protocolVersion": PROTOCOL_VERSION})


_EVENT_SCHEMA = {
    vol.Required("instanceId"): str,
    vol.Required("epoch"): str,
    vol.Required("sequence"): vol.All(int, vol.Range(min=1)),
}


@websocket_api.websocket_command(
    {vol.Required("type"): WS_STATE, **_EVENT_SCHEMA, vol.Required("light"): dict}
)
@websocket_api.require_admin
@websocket_api.async_response
async def websocket_state(hass, connection, msg) -> None:
    """Apply a physical or command-result state event."""
    await _handle_event(hass, connection, msg)


@websocket_api.websocket_command(
    {
        vol.Required("type"): WS_DEVICE_CONNECTED,
        **_EVENT_SCHEMA,
        vol.Required("light"): dict,
    }
)
@websocket_api.require_admin
@websocket_api.async_response
async def websocket_device_connected(hass, connection, msg) -> None:
    """Add or reconnect a dynamic device."""
    await _handle_event(hass, connection, msg)


@websocket_api.websocket_command(
    {
        vol.Required("type"): WS_DEVICE_DISCONNECTED,
        **_EVENT_SCHEMA,
        vol.Required("deviceId"): str,
        vol.Optional("error"): str,
    }
)
@websocket_api.require_admin
@websocket_api.async_response
async def websocket_device_disconnected(hass, connection, msg) -> None:
    """Mark one USB device unavailable."""
    await _handle_event(hass, connection, msg)


async def _handle_event(hass, connection, msg) -> None:
    try:
        await _hub(hass).async_apply_state(connection, msg)
    except (HomeAssistantError, ValueError, TypeError) as err:
        _send_handler_error(connection, msg, err)
        return
    connection.send_result(msg["id"])


@websocket_api.websocket_command(
    {
        vol.Required("type"): WS_COMMAND_RESULT,
        vol.Required("instanceId"): str,
        vol.Required("epoch"): str,
        vol.Required("requestId"): str,
        vol.Required("success"): bool,
        vol.Optional("error"): str,
        vol.Optional("light"): vol.Any(dict, None),
    }
)
@websocket_api.require_admin
@websocket_api.async_response
async def websocket_command_result(hass, connection, msg) -> None:
    """Resolve a command sent by a Home Assistant entity."""
    try:
        await _hub(hass).async_command_result(connection, msg)
    except (HomeAssistantError, ValueError, TypeError) as err:
        _send_handler_error(connection, msg, err)
        return
    connection.send_result(msg["id"])


@callback
def async_register_websocket_api(hass: HomeAssistant) -> None:
    """Register all custom command handlers."""
    websocket_api.async_register_command(hass, websocket_info)
    websocket_api.async_register_command(hass, websocket_subscribe)
    websocket_api.async_register_command(hass, websocket_state)
    websocket_api.async_register_command(hass, websocket_device_connected)
    websocket_api.async_register_command(hass, websocket_device_disconnected)
    websocket_api.async_register_command(hass, websocket_command_result)
