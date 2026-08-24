"""Tests for daemon sessions, state propagation, and entity commands."""

from __future__ import annotations

import asyncio
import json
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import patch

import pytest

from homeassistant.core import Context
from homeassistant.exceptions import HomeAssistantError
from homeassistant.exceptions import Unauthorized
from homeassistant.setup import async_setup_component

from custom_components.elgatolight.bridge import BridgeHub, websocket_info
from custom_components.elgatolight.const import (
    DOMAIN,
    PROTOCOL_VERSION,
    WS_DEVICE_CONNECTED,
    WS_INFO,
    WS_DEVICE_DISCONNECTED,
    WS_STATE,
)
from custom_components.elgatolight.light import ElgatoBridgeLight
from custom_components.elgatolight.scene import (
    ElgatoPresetScene,
    async_setup_entry as async_setup_scene_entry,
)

pytestmark = pytest.mark.asyncio


DEVICE = {
    "id": "A7BTB4251316ZB",
    "stableId": True,
    "name": "Desk key light",
    "manufacturer": "Elgato",
    "model": "Key Light Neo",
    "firmware": "1.0.4",
    "available": True,
    "state": {"on": True, "brightness": 40, "temperature": 3300},
    "capabilities": {
        "minBrightness": 0,
        "maxBrightness": 100,
        "minKelvin": 2900,
        "maxKelvin": 7000,
    },
}


class FakeConnection:
    """Small ActiveConnection stand-in used at the hub boundary."""

    def __init__(self) -> None:
        self.events: list[tuple[int, dict]] = []

    def send_event(self, subscription_id: int, event: dict) -> None:
        self.events.append((subscription_id, event))


async def test_websocket_info_command_is_registered(hass, hass_ws_client) -> None:
    """The installed component exposes the daemon protocol over authenticated WS."""
    assert await async_setup_component(hass, DOMAIN, {})
    client = await hass_ws_client(hass)
    await client.send_json_auto_id({"type": WS_INFO})
    result = await client.receive_json()
    assert result["success"] is True
    assert result["result"]["protocolVersion"] == PROTOCOL_VERSION


async def test_diagnostics_contain_no_transport_or_credentials(hass) -> None:
    """Diagnostics expose bridge state but no connection or OAuth material."""
    hub = BridgeHub(hass)
    await hub.async_load()
    await hub.async_register(FakeConnection(), 7, subscription())
    diagnostic = hub.diagnostics()
    assert diagnostic["protocolVersion"] == PROTOCOL_VERSION
    assert diagnostic["devices"][0]["id"] == DEVICE["id"]
    rendered = repr(diagnostic).lower()
    assert "refresh" not in rendered
    assert "access_token" not in rendered


async def test_cross_language_protocol_fixture(hass) -> None:
    """Consume the same versioned messages as the Go bridge tests."""
    fixture_path = Path(__file__).parents[1] / "testdata" / "bridge_protocol.json"
    fixture = json.loads(fixture_path.read_text(encoding="utf-8"))
    assert fixture["protocolVersion"] == PROTOCOL_VERSION
    messages = fixture["messages"]

    hub = BridgeHub(hass)
    await hub.async_load()
    connection = FakeConnection()
    await hub.async_register(connection, 77, messages["subscribe"])
    await hub.async_apply_state(connection, messages["state"])
    assert hub.device("SERIAL-FIXTURE")["state"]["brightness"] == 32
    await hub.async_apply_state(connection, messages["disconnect"])
    assert hub.device("SERIAL-FIXTURE")["available"] is False

    assert messages["command"]["update"] == {
        "on": True,
        "brightness": 32,
        "temperature": 4500,
    }
    assert messages["presetCommand"]["preset"] == 2
    assert messages["commandResult"]["requestId"] == messages["command"]["requestId"]
    assert messages["resync"]["event"] == "resync"


def subscription(
    devices=None,
    *,
    sequence: int = 0,
    instance_id: str = "daemon-1",
    epoch: str = "epoch-1",
) -> dict:
    """Return a valid daemon subscription payload."""
    return {
        "protocolVersion": PROTOCOL_VERSION,
        "instanceId": instance_id,
        "epoch": epoch,
        "sequence": sequence,
        "daemonVersion": "test",
        "devices": [DEVICE] if devices is None else devices,
    }


async def test_websocket_commands_require_an_administrator(hass) -> None:
    """A normal authenticated HA user cannot impersonate a daemon."""
    connection = SimpleNamespace(user=SimpleNamespace(is_admin=False))
    with pytest.raises(Unauthorized):
        websocket_info(hass, connection, {"id": 1, "type": WS_INFO})


async def test_instances_and_duplicate_sessions_are_isolated(hass) -> None:
    """Newest same-instance session wins without affecting another daemon."""
    hub = BridgeHub(hass)
    await hub.async_load()
    first = FakeConnection()
    first_session = await hub.async_register(first, 1, subscription())

    second_device = {**DEVICE, "id": "SERIAL-B", "name": "Second light"}
    other = FakeConnection()
    other_session = await hub.async_register(
        other,
        2,
        subscription([second_device], instance_id="daemon-2", epoch="epoch-2"),
    )
    replacement = FakeConnection()
    replacement_session = await hub.async_register(
        replacement, 3, subscription(epoch="epoch-replacement")
    )

    assert first_session.active is False
    assert replacement_session.active is True
    with pytest.raises(HomeAssistantError, match="stale"):
        await hub.async_apply_state(
            first,
            {
                "type": WS_STATE,
                "instanceId": "daemon-1",
                "epoch": "epoch-1",
                "sequence": 1,
                "light": DEVICE,
            },
        )

    await hub.async_disconnect(replacement_session)
    assert hub.device(DEVICE["id"])["available"] is False
    assert hub.device("SERIAL-B")["available"] is True
    assert other_session.active is True


async def test_snapshot_state_disconnect_and_persistence(hass) -> None:
    """Daemon snapshots and sequenced events update retained inventory."""
    hub = BridgeHub(hass)
    await hub.async_load()
    connection = FakeConnection()
    session = await hub.async_register(connection, 12, subscription())

    assert hub.device_ids() == [DEVICE["id"]]
    assert hub.device(DEVICE["id"])["available"] is True

    changed = dict(DEVICE)
    changed["state"] = {"on": False, "brightness": 75, "temperature": 4500}
    await hub.async_apply_state(
        connection,
        {
            "type": WS_STATE,
            "instanceId": "daemon-1",
            "epoch": "epoch-1",
            "sequence": 1,
            "light": changed,
        },
    )
    assert hub.device(DEVICE["id"])["state"] == changed["state"]

    await hub.async_apply_state(
        connection,
        {
            "type": WS_DEVICE_DISCONNECTED,
            "instanceId": "daemon-1",
            "epoch": "epoch-1",
            "sequence": 2,
            "deviceId": DEVICE["id"],
            "error": "USB unplugged",
        },
    )
    assert hub.device(DEVICE["id"])["available"] is False
    assert hub.device(DEVICE["id"])["error"] == "USB unplugged"

    restored = BridgeHub(hass)
    await restored.async_load()
    assert restored.device_ids() == [DEVICE["id"]]
    assert restored.device(DEVICE["id"])["available"] is False

    await hub.async_disconnect(session)


async def test_sequence_gap_requests_resync(hass) -> None:
    """A missed daemon event fails closed and requests a new snapshot."""
    hub = BridgeHub(hass)
    await hub.async_load()
    connection = FakeConnection()
    await hub.async_register(connection, 22, subscription(sequence=4))

    with pytest.raises(HomeAssistantError, match="sequence gap"):
        await hub.async_apply_state(
            connection,
            {
                "type": WS_STATE,
                "instanceId": "daemon-1",
                "epoch": "epoch-1",
                "sequence": 6,
                "light": DEVICE,
            },
        )
    assert connection.events == [
        (22, {"event": "resync", "reason": "expected sequence 5, got 6"})
    ]


async def test_entity_command_is_correlated_and_scales_brightness(hass) -> None:
    """HA actions travel over the outbound subscription and await a result."""
    hub = BridgeHub(hass)
    await hub.async_load()
    connection = FakeConnection()
    await hub.async_register(connection, 32, subscription())
    entity = ElgatoBridgeLight(hub, DEVICE["id"])

    assert entity.available is True
    assert entity.is_on is True
    assert entity.brightness == 102
    assert entity.color_temp_kelvin == 3300

    command_task = asyncio.create_task(
        entity.async_turn_on(brightness=204, color_temp_kelvin=5000)
    )
    await asyncio.sleep(0)
    subscription_id, command = connection.events.pop()
    assert subscription_id == 32
    assert command["event"] == "command"
    assert command["deviceId"] == DEVICE["id"]
    assert command["update"] == {"on": True, "brightness": 80, "temperature": 5000}

    updated = dict(DEVICE)
    updated["state"] = {"on": True, "brightness": 80, "temperature": 5000}
    await hub.async_command_result(
        connection,
        {
            "instanceId": "daemon-1",
            "epoch": "epoch-1",
            "requestId": command["requestId"],
            "success": True,
            "light": updated,
        },
    )
    await command_task
    assert hub.device(DEVICE["id"])["state"] == updated["state"]


async def test_preset_scenes_recall_hardware_slots(hass) -> None:
    """Both scenes send preset actions and apply returned state."""
    hub = BridgeHub(hass)
    await hub.async_load()
    connection = FakeConnection()
    await hub.async_register(connection, 33, subscription())

    scenes = [ElgatoPresetScene(hub, DEVICE["id"], preset) for preset in (1, 2)]
    assert [scene.unique_id for scene in scenes] == [
        f"{DEVICE['id']}_preset_1_scene",
        f"{DEVICE['id']}_preset_2_scene",
    ]
    assert [scene.translation_key for scene in scenes] == ["preset_1", "preset_2"]
    assert [scene.icon for scene in scenes] == [
        "mdi:numeric-1-box",
        "mdi:numeric-2-box",
    ]
    assert all(
        scene.device_info["identifiers"] == {(DOMAIN, DEVICE["id"])}
        for scene in scenes
    )
    assert all(scene.available is True for scene in scenes)

    for scene, preset, brightness in zip(scenes, (1, 2), (25, 65), strict=True):
        command_task = asyncio.create_task(scene.async_activate())
        await asyncio.sleep(0)
        subscription_id, command = connection.events.pop()
        assert subscription_id == 33
        assert command["event"] == "command"
        assert command["deviceId"] == DEVICE["id"]
        assert command["preset"] == preset
        assert "update" not in command

        updated = dict(DEVICE)
        updated["state"] = {
            "on": True,
            "brightness": brightness,
            "temperature": 3000 + preset * 500,
        }
        await hub.async_command_result(
            connection,
            {
                "instanceId": "daemon-1",
                "epoch": "epoch-1",
                "requestId": command["requestId"],
                "success": True,
                "light": updated,
            },
        )
        await command_task
        assert hub.device(DEVICE["id"])["state"] == updated["state"]

    with pytest.raises(HomeAssistantError, match="preset must be 1 or 2"):
        await hub.async_apply_preset(DEVICE["id"], 3)


async def test_preset_context_reaches_light_and_physical_updates_are_neutral(hass) -> None:
    """Logbook gets the initiating action without retaining stale contexts."""
    hub = BridgeHub(hass)
    await hub.async_load()
    connection = FakeConnection()
    await hub.async_register(connection, 35, subscription())
    light = ElgatoBridgeLight(hub, DEVICE["id"])
    scene = ElgatoPresetScene(hub, DEVICE["id"], 1)

    with patch.object(light, "async_write_ha_state") as write_state:
        unsubscribe = hub.subscribe_device(DEVICE["id"], light._handle_model_update)
        scene_context = Context(user_id="scene-user")
        scene.async_set_context(scene_context)
        command_task = asyncio.create_task(scene.async_activate())
        await asyncio.sleep(0)
        command = connection.events.pop()[1]

        preset_state = {
            **DEVICE,
            "state": {"on": True, "brightness": 25, "temperature": 3500},
        }
        await hub.async_command_result(
            connection,
            {
                "instanceId": "daemon-1",
                "epoch": "epoch-1",
                "requestId": command["requestId"],
                "success": True,
                "light": preset_state,
            },
        )
        await command_task

        assert light._context is scene_context
        write_state.assert_called_once()

        # The daemon publishes the same authoritative state after its command
        # result. It must not erase the scene attribution with a neutral context.
        await hub.async_apply_state(
            connection,
            {
                "type": WS_STATE,
                "instanceId": "daemon-1",
                "epoch": "epoch-1",
                "sequence": 1,
                "light": preset_state,
            },
        )
        assert light._context is scene_context
        write_state.assert_called_once()

        physical_state = {
            **preset_state,
            "state": {"on": False, "brightness": 25, "temperature": 3500},
        }
        await hub.async_apply_state(
            connection,
            {
                "type": WS_STATE,
                "instanceId": "daemon-1",
                "epoch": "epoch-1",
                "sequence": 2,
                "light": physical_state,
            },
        )
        assert light._context is not scene_context
        assert light._context is not None
        assert light._context.user_id is None
        assert write_state.call_count == 2
        unsubscribe()


async def test_preset_scene_platform_follows_dynamic_inventory(hass) -> None:
    """The scene platform follows retained and newly found lights."""
    hub = BridgeHub(hass)
    await hub.async_load()
    connection = FakeConnection()
    await hub.async_register(connection, 34, subscription())

    unloads = []
    entry = SimpleNamespace(runtime_data=hub, async_on_unload=unloads.append)
    scenes = []
    await async_setup_scene_entry(hass, entry, scenes.extend)

    assert {entity.unique_id for entity in scenes} == {
        f"{DEVICE['id']}_preset_1_scene",
        f"{DEVICE['id']}_preset_2_scene",
    }

    second_device = {**DEVICE, "id": "SERIAL-B", "name": "Second light"}
    await hub.async_apply_state(
        connection,
        {
            "type": WS_DEVICE_CONNECTED,
            "instanceId": "daemon-1",
            "epoch": "epoch-1",
            "sequence": 1,
            "light": second_device,
        },
    )

    assert len(scenes) == 4
    assert {entity.unique_id for entity in scenes if "SERIAL-B" in entity.unique_id} == {
        "SERIAL-B_preset_1_scene",
        "SERIAL-B_preset_2_scene",
    }

    for unload in unloads:
        unload()


@pytest.mark.parametrize(
    "device",
    [
        {**DEVICE, "id": ""},
        {**DEVICE, "state": {"on": True, "brightness": 101, "temperature": 3300}},
        {**DEVICE, "state": {"on": True, "brightness": 40, "temperature": 8000}},
    ],
)
async def test_invalid_device_payload_is_rejected(hass, device) -> None:
    """Untrusted daemon payloads cannot create malformed HA entities."""
    hub = BridgeHub(hass)
    await hub.async_load()
    with pytest.raises(HomeAssistantError):
        await hub.async_register(FakeConnection(), 42, subscription([device]))
    assert hub.diagnostics()["sessions"] == []


async def test_duplicate_snapshot_ids_are_rejected_transactionally(hass) -> None:
    """A malformed snapshot cannot replace the currently valid session."""
    hub = BridgeHub(hass)
    await hub.async_load()
    valid = FakeConnection()
    session = await hub.async_register(valid, 51, subscription())
    with pytest.raises(HomeAssistantError, match="duplicate device id"):
        await hub.async_register(
            FakeConnection(),
            52,
            subscription([DEVICE, DEVICE], epoch="bad-replacement"),
        )
    assert session.active is True
    assert hub.diagnostics()["sessions"][0]["epoch"] == "epoch-1"


async def test_command_result_must_match_requested_device(hass) -> None:
    """A correlated result cannot update a different entity."""
    hub = BridgeHub(hass)
    await hub.async_load()
    connection = FakeConnection()
    await hub.async_register(connection, 61, subscription())
    command_task = asyncio.create_task(hub.async_command(DEVICE["id"], {"on": False}))
    await asyncio.sleep(0)
    command = connection.events.pop()[1]
    wrong = {**DEVICE, "id": "SERIAL-WRONG"}
    with pytest.raises(HomeAssistantError, match="does not match"):
        await hub.async_command_result(
            connection,
            {
                "instanceId": "daemon-1",
                "epoch": "epoch-1",
                "requestId": command["requestId"],
                "success": True,
                "light": wrong,
            },
        )
    with pytest.raises(HomeAssistantError, match="does not match"):
        await command_task
