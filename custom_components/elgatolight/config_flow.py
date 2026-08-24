"""Config flow for the Elgato USB Light Bridge integration."""

from __future__ import annotations

from typing import Any

import voluptuous as vol

from homeassistant import config_entries
from homeassistant.data_entry_flow import FlowResult

from .const import DOMAIN, INTEGRATION_NAME


class ElgatoLightConfigFlow(config_entries.ConfigFlow, domain=DOMAIN):
    """Create the singleton receiver used by outbound daemon connections."""

    VERSION = 1

    async def async_step_user(
        self, user_input: dict[str, Any] | None = None
    ) -> FlowResult:
        """Enable the receiver; it has no HA-to-daemon network settings."""
        if self._async_current_entries():
            return self.async_abort(reason="single_instance_allowed")
        if user_input is not None:
            return self.async_create_entry(title=INTEGRATION_NAME, data={})
        return self.async_show_form(step_id="user", data_schema=vol.Schema({}))
