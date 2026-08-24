package homeassistant

import (
	"encoding/json"
	"os"
	"testing"
)

func TestCrossLanguageProtocolFixture(t *testing.T) {
	data, err := os.ReadFile("../../testdata/bridge_protocol.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		ProtocolVersion int                        `json:"protocolVersion"`
		Messages        map[string]json.RawMessage `json:"messages"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.ProtocolVersion != ProtocolVersion {
		t.Fatalf("fixture protocol = %d, implementation = %d", fixture.ProtocolVersion, ProtocolVersion)
	}

	var subscribe SubscribeCommand
	if err := json.Unmarshal(fixture.Messages["subscribe"], &subscribe); err != nil {
		t.Fatal(err)
	}
	if subscribe.Type != TypeSubscribe || subscribe.Sequence != 41 || len(subscribe.Devices) != 1 || subscribe.Devices[0].ID != "SERIAL-FIXTURE" {
		t.Fatalf("unexpected subscription fixture: %#v", subscribe)
	}
	var state StateCommand
	if err := json.Unmarshal(fixture.Messages["state"], &state); err != nil {
		t.Fatal(err)
	}
	if state.Type != TypeState || state.Sequence != 42 || state.Light.State.Brightness != 32 {
		t.Fatalf("unexpected state fixture: %#v", state)
	}
	var disconnect DisconnectCommand
	if err := json.Unmarshal(fixture.Messages["disconnect"], &disconnect); err != nil {
		t.Fatal(err)
	}
	if disconnect.Type != TypeDeviceDisconnected || disconnect.DeviceID != state.Light.ID {
		t.Fatalf("unexpected disconnect fixture: %#v", disconnect)
	}
	var command SubscriptionEvent
	if err := json.Unmarshal(fixture.Messages["command"], &command); err != nil {
		t.Fatal(err)
	}
	if command.Event != "command" || command.Update.Brightness == nil || *command.Update.Brightness != 32 {
		t.Fatalf("unexpected command fixture: %#v", command)
	}
	var presetCommand SubscriptionEvent
	if err := json.Unmarshal(fixture.Messages["presetCommand"], &presetCommand); err != nil {
		t.Fatal(err)
	}
	if presetCommand.Event != "command" || presetCommand.Preset == nil || *presetCommand.Preset != 2 {
		t.Fatalf("unexpected preset command fixture: %#v", presetCommand)
	}
	var result CommandResult
	if err := json.Unmarshal(fixture.Messages["commandResult"], &result); err != nil {
		t.Fatal(err)
	}
	if result.Type != TypeCommandResult || !result.Success || result.Light == nil || result.RequestID != command.RequestID {
		t.Fatalf("unexpected result fixture: %#v", result)
	}
	var resync SubscriptionEvent
	if err := json.Unmarshal(fixture.Messages["resync"], &resync); err != nil {
		t.Fatal(err)
	}
	if resync.Event != "resync" || resync.Reason == "" {
		t.Fatalf("unexpected resync fixture: %#v", resync)
	}
}
