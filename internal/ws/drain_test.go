package ws

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestDrainSendsGoingAwayAndWaitsForPumps(t *testing.T) {
	hub := New()
	go hub.Run()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _ = hub.Upgrade(w, r, "drain-client", 7) }))
	defer server.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	select {
	case <-hub.registered:
	case <-time.After(time.Second):
		t.Fatal("not registered")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := hub.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	_, _, err = conn.ReadMessage()
	if !websocket.IsCloseError(err, websocket.CloseGoingAway) {
		t.Fatalf("close=%v, want1001", err)
	}
	if hub.Ready() {
		t.Fatal("drained hub ready")
	}
	if err := hub.admitConnection(ctx, 7); !errors.Is(err, errHubStopped) {
		t.Fatalf("new admission=%v", err)
	}
	if err := hub.Drain(ctx); err != nil {
		t.Fatal("repeated drain failed", err)
	}
}

func TestDrainCancelsPendingAuthorization(t *testing.T) {
	started := make(chan struct{})
	cancelled := make(chan struct{})
	hub, client := startTestHub(t, contextAuthorizer{started: started, cancelled: cancelled})
	sendCommand(hub, client, Message{Type: EventJoin, RoomID: "1"})
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("authorization not started")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := hub.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-cancelled:
	default:
		t.Fatal("authorization outlived drain")
	}
}

type contextAuthorizer struct{ started, cancelled chan struct{} }

func (a contextAuthorizer) AuthorizeRoom(ctx context.Context, _, _ int64) error {
	close(a.started)
	<-ctx.Done()
	close(a.cancelled)
	return ctx.Err()
}

func TestDrainDeadlineForcesSlowWriterToReleasePumps(t *testing.T) {
	hub := New()
	go hub.Run()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _ = hub.Upgrade(w, r, "slow-writer", 7) }))
	defer server.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var client *Client
	select {
	case client = <-hub.registered:
	case <-time.After(time.Second):
		t.Fatal("not registered")
	}
	if tcp, ok := client.conn.UnderlyingConn().(*net.TCPConn); ok {
		if err := tcp.SetWriteBuffer(1024); err != nil {
			t.Fatal(err)
		}
	}
	if tcp, ok := conn.UnderlyingConn().(*net.TCPConn); ok {
		if err := tcp.SetReadBuffer(1024); err != nil {
			t.Fatal(err)
		}
	}
	// The peer deliberately does not read; this cannot fit in its TCP buffers.
	client.send <- outboundMessage{data: bytes.Repeat([]byte("x"), 4<<20)}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := hub.Drain(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("slow drain=%v", err)
	}
	finished := make(chan struct{})
	go func() { hub.pumps.Wait(); close(finished) }()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("forced close leaked pumps")
	}
}

func TestCancelledInboundReaderDoesNotPreemptDrainCloseFrame(t *testing.T) {
	hub := New()
	hub.inbound = make(chan inboundMessage)
	clients := make(chan *Client, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := hub.upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		clients <- newClient("cancelled-inbound", 7, conn, hub, r.Context())
	}))
	defer server.Close()
	peer, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	var client *Client
	select {
	case client = <-clients:
	case <-time.After(time.Second):
		t.Fatal("no client")
	}
	readerDone := make(chan struct{})
	go func() { client.ReadPump(); close(readerDone) }()
	if err := peer.WriteJSON(Message{Type: EventJoin, RoomID: "1"}); err != nil {
		t.Fatal(err)
	}
	client.shutdownClose.Store(true)
	client.cancel()
	select {
	case <-readerDone:
	case <-time.After(time.Second):
		t.Fatal("reader cancellation leaked")
	}
	close(client.send)
	writerDone := make(chan struct{})
	go func() { client.WritePump(); close(writerDone) }()
	_ = peer.SetReadDeadline(time.Now().Add(time.Second))
	_, _, err = peer.ReadMessage()
	if !websocket.IsCloseError(err, websocket.CloseGoingAway) {
		t.Fatalf("reader preempted 1001: %v", err)
	}
	select {
	case <-writerDone:
	case <-time.After(time.Second):
		t.Fatal("writer leaked")
	}
}

// Delay the loop's start to deterministically exercise cancellation before its
// stopped signal. No maps are touched after Run starts.
func TestExpiredDrainBudgetStillReleasesWriterAfterLoopStops(t *testing.T) {
	hub := New()
	clients := make(chan *Client, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := hub.upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		clients <- newClient("expired-drain", 7, conn, hub, r.Context())
	}))
	defer server.Close()
	peer, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	var client *Client
	select {
	case client = <-clients:
	case <-time.After(time.Second):
		t.Fatal("no client")
	}
	if tcp, ok := client.conn.UnderlyingConn().(*net.TCPConn); ok {
		if err := tcp.SetWriteBuffer(1024); err != nil {
			t.Fatal(err)
		}
	}
	if tcp, ok := peer.UnderlyingConn().(*net.TCPConn); ok {
		if err := tcp.SetReadBuffer(1024); err != nil {
			t.Fatal(err)
		}
	}
	hub.clients[client.ID] = client
	client.send <- outboundMessage{data: bytes.Repeat([]byte("x"), 4<<20)}
	hub.pumps.Add(1)
	go func() { defer hub.pumps.Done(); client.WritePump() }()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := hub.Drain(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled drain=%v", err)
	}
	go hub.Run()
	finished := make(chan struct{})
	go func() { hub.pumps.Wait(); close(finished) }()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("expired drain budget left writer alive")
	}
	select {
	case <-hub.stopped:
	case <-time.After(time.Second):
		t.Fatal("loop did not stop")
	}
}

func TestBroadcastAfterShutdownIsRejectedWithoutEnqueue(t *testing.T) {
	hub := New()
	hub.Shutdown()
	for i := 0; i < 512; i++ {
		if err := hub.Broadcast("1", Message{Type: EventMessage}); !errors.Is(err, errHubStopped) {
			t.Fatal("room broadcast accepted after shutdown")
		}
		if err := hub.BroadcastToUser(7, Message{Type: EventMessage}); !errors.Is(err, errHubStopped) {
			t.Fatal("user broadcast accepted after shutdown")
		}
	}
	if len(hub.broadcast) != 0 || len(hub.userBroadcast) != 0 {
		t.Fatal("events queued with no consumer")
	}
}

func TestDrainWithConcurrentUpgradeAndDisconnect(t *testing.T) {
	hub := New(Options{MaxConnectionsPerUser: 64, AllowMissingOrigin: true})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := hub.Drain(ctx); err != nil {
			t.Error(err)
		}
	})
	go hub.Run()
	var sequence atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = hub.Upgrade(w, r, "parallel-"+strconv.FormatInt(sequence.Add(1), 10), 7)
	}))
	defer server.Close()
	url := "ws" + strings.TrimPrefix(server.URL, "http")
	dialer := websocket.Dialer{HandshakeTimeout: time.Second}
	peer, _, err := dialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	select {
	case <-hub.registered:
	case <-time.After(time.Second):
		t.Fatal("initial client not registered")
	}
	start := make(chan struct{})
	failures := make(chan error, 8)
	var workers sync.WaitGroup
	workers.Add(9)
	go func() { defer workers.Done(); <-start; _ = peer.Close() }()
	for i := 0; i < 8; i++ {
		go func() {
			defer workers.Done()
			<-start
			conn, response, err := dialer.Dial(url, nil)
			if err == nil {
				_ = conn.Close()
				return
			}
			if response != nil {
				defer response.Body.Close()
			}
			if response == nil || response.StatusCode != http.StatusServiceUnavailable {
				failures <- err
			}
		}()
	}
	close(start)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := hub.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	finished := make(chan struct{})
	go func() { workers.Wait(); close(finished) }()
	select {
	case <-finished:
	case <-ctx.Done():
		t.Fatal("upgrade/disconnect work leaked")
	}
	if len(failures) > 0 {
		t.Fatal("upgrade neither accepted nor rejected with 503", <-failures)
	}
	if hub.Ready() {
		t.Fatal("drained hub accepting connections")
	}
}
