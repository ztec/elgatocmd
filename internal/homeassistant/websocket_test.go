package homeassistant

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestWebSocketClientAuthenticationCallsAndEvents(t *testing.T) {
	t.Parallel()
	serverErrors := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/base/api/websocket" {
			http.NotFound(writer, request)
			return
		}
		connection, err := websocket.Accept(writer, request, nil)
		if err != nil {
			serverErrors <- err
			return
		}
		defer connection.CloseNow()
		ctx := request.Context()
		if err := writeJSON(ctx, connection, map[string]any{"type": "auth_required"}); err != nil {
			serverErrors <- err
			return
		}
		var auth map[string]any
		if err := readJSON(ctx, connection, &auth); err != nil {
			serverErrors <- err
			return
		}
		if auth["type"] != "auth" || auth["access_token"] != "token" {
			serverErrors <- fmt.Errorf("unexpected auth request: %#v", auth)
			return
		}
		if err := writeJSON(ctx, connection, map[string]any{"type": "auth_ok"}); err != nil {
			serverErrors <- err
			return
		}

		var info map[string]any
		if err := readJSON(ctx, connection, &info); err != nil {
			serverErrors <- err
			return
		}
		if info["type"] != TypeInfo {
			serverErrors <- fmt.Errorf("unexpected info command: %#v", info)
			return
		}
		if err := writeJSON(ctx, connection, map[string]any{
			"id": info["id"], "type": "result", "success": true,
			"result": map[string]any{"protocolVersion": ProtocolVersion},
		}); err != nil {
			serverErrors <- err
			return
		}

		var subscribe map[string]any
		if err := readJSON(ctx, connection, &subscribe); err != nil {
			serverErrors <- err
			return
		}
		if subscribe["type"] != TypeSubscribe {
			serverErrors <- fmt.Errorf("unexpected subscribe command: %#v", subscribe)
			return
		}
		if err := writeJSON(ctx, connection, map[string]any{
			"id": subscribe["id"], "type": "result", "success": true,
			"result": map[string]any{"protocolVersion": ProtocolVersion},
		}); err != nil {
			serverErrors <- err
			return
		}
		if err := writeJSON(ctx, connection, map[string]any{
			"id": subscribe["id"], "type": "event",
			"event": map[string]any{"event": "command", "requestId": "request-1", "deviceId": "light-1"},
		}); err != nil {
			serverErrors <- err
			return
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := DialWebSocket(ctx, server.URL+"/base", "token", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	result, err := client.Call(ctx, map[string]any{"type": TypeInfo})
	if err != nil {
		t.Fatal(err)
	}
	var info struct {
		ProtocolVersion int `json:"protocolVersion"`
	}
	if err := json.Unmarshal(result, &info); err != nil || info.ProtocolVersion != ProtocolVersion {
		t.Fatalf("info result = %s, error = %v", result, err)
	}
	subscriptionID, _, err := client.CallWithID(ctx, map[string]any{"type": TypeSubscribe})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-client.Events():
		if event.SubscriptionID != subscriptionID || !json.Valid(event.Payload) {
			t.Fatalf("unexpected event: %#v", event)
		}
	case err := <-serverErrors:
		t.Fatal(err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}
