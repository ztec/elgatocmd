package homeassistant

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"

	"github.com/coder/websocket"
)

// Event is one server-pushed event on a Home Assistant subscription.
type Event struct {
	SubscriptionID int
	Payload        json.RawMessage
}

type callResponse struct {
	result json.RawMessage
	err    error
}

// WebSocketClient is a small concurrent RPC client for Home Assistant's
// authenticated /api/websocket endpoint.
type WebSocketClient struct {
	conn   *websocket.Conn
	cancel context.CancelFunc

	nextID atomic.Int64
	write  sync.Mutex
	mu     sync.Mutex
	waits  map[int]chan callResponse
	events chan Event
	done   chan error
}

// DialWebSocket authenticates a Home Assistant WebSocket using a short-lived
// access token.
func DialWebSocket(ctx context.Context, rawHAURL, accessToken string, httpClient *http.Client) (*WebSocketClient, error) {
	wsURL, err := websocketURL(rawHAURL)
	if err != nil {
		return nil, err
	}
	options := &websocket.DialOptions{HTTPClient: HTTPClientWithTransport(httpClient)}
	conn, response, err := websocket.Dial(ctx, wsURL.String(), options)
	if err != nil {
		if response != nil {
			return nil, fmt.Errorf("connect Home Assistant WebSocket (HTTP %s): %w", response.Status, err)
		}
		return nil, fmt.Errorf("connect Home Assistant WebSocket: %w", err)
	}
	conn.SetReadLimit(2 << 20)
	closeWithError := func(err error) (*WebSocketClient, error) {
		_ = conn.Close(websocket.StatusPolicyViolation, "authentication failed")
		return nil, err
	}

	var required struct {
		Type string `json:"type"`
	}
	if err := readJSON(ctx, conn, &required); err != nil {
		return closeWithError(fmt.Errorf("read Home Assistant authentication greeting: %w", err))
	}
	if required.Type != "auth_required" {
		return closeWithError(fmt.Errorf("unexpected Home Assistant greeting %q", required.Type))
	}
	authRequest := map[string]any{"type": "auth", "access_token": accessToken}
	if err := writeJSON(ctx, conn, authRequest); err != nil {
		return closeWithError(fmt.Errorf("send Home Assistant authentication: %w", err))
	}
	var auth struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	}
	if err := readJSON(ctx, conn, &auth); err != nil {
		return closeWithError(fmt.Errorf("read Home Assistant authentication result: %w", err))
	}
	if auth.Type != "auth_ok" {
		return closeWithError(fmt.Errorf("Home Assistant WebSocket authentication failed: %s", auth.Message))
	}

	readCtx, cancel := context.WithCancel(context.Background())
	client := &WebSocketClient{
		conn: conn, cancel: cancel, waits: make(map[int]chan callResponse),
		events: make(chan Event, 64), done: make(chan error, 1),
	}
	go client.readLoop(readCtx)
	return client, nil
}

// Call sends one command and waits for its correlated result.
func (c *WebSocketClient) Call(ctx context.Context, command any) (json.RawMessage, error) {
	_, result, err := c.CallWithID(ctx, command)
	return result, err
}

// CallWithID also returns the command ID, which is the subscription ID for a
// successful subscription command.
func (c *WebSocketClient) CallWithID(ctx context.Context, command any) (int, json.RawMessage, error) {
	payload, err := json.Marshal(command)
	if err != nil {
		return 0, nil, err
	}
	var message map[string]any
	if err := json.Unmarshal(payload, &message); err != nil {
		return 0, nil, err
	}
	id := int(c.nextID.Add(1))
	message["id"] = id
	wait := make(chan callResponse, 1)
	c.mu.Lock()
	c.waits[id] = wait
	c.mu.Unlock()
	if err := c.writeJSON(ctx, message); err != nil {
		c.removeWait(id)
		return id, nil, err
	}
	select {
	case response := <-wait:
		return id, response.result, response.err
	case <-ctx.Done():
		c.removeWait(id)
		return id, nil, ctx.Err()
	}
}

// Events returns server-pushed subscription events.
func (c *WebSocketClient) Events() <-chan Event { return c.events }

// Done receives the terminal read error and then closes.
func (c *WebSocketClient) Done() <-chan error { return c.done }

func (c *WebSocketClient) Close() error {
	c.cancel()
	return c.conn.Close(websocket.StatusNormalClosure, "daemon stopping")
}

func (c *WebSocketClient) readLoop(ctx context.Context) {
	err := c.readMessages(ctx)
	c.mu.Lock()
	for id, wait := range c.waits {
		wait <- callResponse{err: err}
		delete(c.waits, id)
	}
	c.mu.Unlock()
	close(c.events)
	c.done <- err
	close(c.done)
}

func (c *WebSocketClient) readMessages(ctx context.Context) error {
	for {
		_, data, err := c.conn.Read(ctx)
		if err != nil {
			if ctx.Err() != nil || websocket.CloseStatus(err) == websocket.StatusNormalClosure {
				return nil
			}
			return fmt.Errorf("read Home Assistant WebSocket: %w", err)
		}
		var envelope struct {
			ID      int             `json:"id"`
			Type    string          `json:"type"`
			Success bool            `json:"success"`
			Result  json.RawMessage `json:"result"`
			Event   json.RawMessage `json:"event"`
			Error   *struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(data, &envelope); err != nil {
			return fmt.Errorf("decode Home Assistant WebSocket message: %w", err)
		}
		switch envelope.Type {
		case "result":
			c.mu.Lock()
			wait := c.waits[envelope.ID]
			delete(c.waits, envelope.ID)
			c.mu.Unlock()
			if wait == nil {
				continue
			}
			if !envelope.Success {
				message := "unknown error"
				code := "unknown_error"
				if envelope.Error != nil {
					message, code = envelope.Error.Message, envelope.Error.Code
				}
				wait <- callResponse{err: fmt.Errorf("Home Assistant command failed (%s): %s", code, message)}
			} else {
				wait <- callResponse{result: envelope.Result}
			}
		case "event":
			select {
			case c.events <- Event{SubscriptionID: envelope.ID, Payload: envelope.Event}:
			case <-ctx.Done():
				return nil
			}
		default:
			return fmt.Errorf("unexpected Home Assistant WebSocket message type %q", envelope.Type)
		}
	}
}

func (c *WebSocketClient) writeJSON(ctx context.Context, value any) error {
	c.write.Lock()
	defer c.write.Unlock()
	return writeJSON(ctx, c.conn, value)
}

func (c *WebSocketClient) removeWait(id int) {
	c.mu.Lock()
	delete(c.waits, id)
	c.mu.Unlock()
}

func readJSON(ctx context.Context, connection *websocket.Conn, destination any) error {
	_, data, err := connection.Read(ctx)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, destination)
}

func writeJSON(ctx context.Context, connection *websocket.Conn, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return connection.Write(ctx, websocket.MessageText, data)
}

// HTTPClientWithTransport returns a default client when nil. Kept separate so
// callers can inject TLS and test transports without weakening production.
func HTTPClientWithTransport(client *http.Client) *http.Client {
	if client != nil {
		return client
	}
	return http.DefaultClient
}

func websocketURL(rawHAURL string) (*url.URL, error) {
	parsed, err := NormalizeURL(rawHAURL)
	if err != nil {
		return nil, err
	}
	if parsed.Scheme == "https" {
		parsed.Scheme = "wss"
	} else {
		parsed.Scheme = "ws"
	}
	parsed.Path = joinURLPath(parsed.Path, "/api/websocket")
	return parsed, nil
}
