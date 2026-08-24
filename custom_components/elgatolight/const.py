"""Constants for the Elgato USB Light Bridge integration."""

from typing import Final

DOMAIN: Final = "elgatolight"
INTEGRATION_NAME: Final = "Elgato USB Light Bridge"
PROTOCOL_VERSION: Final = 2
STORAGE_KEY: Final = f"{DOMAIN}.devices"
STORAGE_VERSION: Final = 1
COMMAND_TIMEOUT: Final = 10.0

WS_INFO: Final = f"{DOMAIN}/info"
WS_SUBSCRIBE: Final = f"{DOMAIN}/subscribe"
WS_STATE: Final = f"{DOMAIN}/state"
WS_DEVICE_CONNECTED: Final = f"{DOMAIN}/device_connected"
WS_DEVICE_DISCONNECTED: Final = f"{DOMAIN}/device_disconnected"
WS_COMMAND_RESULT: Final = f"{DOMAIN}/command_result"
