package ws

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestReadPumpReportsPayloadTooLarge(t *testing.T) {
	hub := New()
	serverClient := make(chan *Client, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		client := newClient("client-1", 42, conn, hub, context.Background())
		serverClient <- client
		go client.ReadPump()
	}))
	defer server.Close()

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.Close()
	<-serverClient

	if err := conn.WriteMessage(websocket.TextMessage, bytes.Repeat([]byte("x"), maxMessageSize+1)); err != nil {
		t.Fatalf("WriteMessage() error = %v", err)
	}

	select {
	case msg := <-hub.inbound:
		if msg.ErrorCode != "payload_too_large" {
			t.Fatalf("inbound error code = %q, want payload_too_large", msg.ErrorCode)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for payload limit error")
	}
}
