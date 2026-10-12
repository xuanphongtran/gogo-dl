// Command smoke performs one bounded Render Free deployment check.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

type options struct {
	base, tokenFile, origin     string
	room, message, notification int64
	timeout                     time.Duration
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "smoke:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, out, errOut io.Writer) error {
	var o options
	f := flag.NewFlagSet("smoke", flag.ContinueOnError)
	f.SetOutput(errOut)
	f.StringVar(&o.base, "url", "", "Service base URL (HTTPS, or HTTP localhost)")
	f.StringVar(&o.tokenFile, "token-file", "", "Access token file outside the repository; enables authenticated checks")
	f.StringVar(&o.origin, "origin", "", "Allowed frontend origin for WebSocket checks")
	f.Int64Var(&o.room, "room", 0, "Existing room joined by the token owner; enables history and two WS joins")
	f.Int64Var(&o.message, "expect-message", 0, "Message ID that must remain in the room's latest 100 messages")
	f.Int64Var(&o.notification, "expect-notification", 0, "Notification ID that must remain in the latest 100 inbox rows")
	f.DurationVar(&o.timeout, "timeout", 120*time.Second, "Timeout per request, allowing a Free cold start (maximum 5m)")
	if err := f.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return errors.New("invalid flags")
	}
	u, err := url.Parse(o.base)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || f.NArg() != 0 {
		return errors.New("provide a base URL without credentials, path, query or fragment")
	}
	local := u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1"
	if u.Scheme != "https" && !(u.Scheme == "http" && local) {
		return errors.New("HTTPS is required except on localhost")
	}
	if o.timeout <= 0 || o.timeout > 5*time.Minute || o.room < 0 || o.message < 0 || o.notification < 0 || (o.message > 0 && o.room == 0) || (o.tokenFile == "" && (o.room > 0 || o.notification > 0)) {
		return errors.New("invalid timeout or authenticated recovery options")
	}
	if o.room > 0 {
		origin, err := url.Parse(o.origin)
		if err != nil || origin.Hostname() == "" || origin.User != nil || origin.RawQuery != "" || origin.Fragment != "" || origin.Path != "" || (origin.Scheme != "https" && origin.Scheme != "http") {
			return errors.New("room checks require an exact allowed HTTP(S) origin")
		}
	}
	var token string
	if o.tokenFile != "" {
		file, err := os.Open(o.tokenFile)
		if err != nil {
			return errors.New("cannot open token file")
		}
		body, readErr := io.ReadAll(io.LimitReader(file, 8193))
		closeErr := file.Close()
		token = strings.TrimSpace(string(body))
		if readErr != nil || closeErr != nil || len(body) > 8192 || token == "" || strings.ContainsAny(token, " \r\n\t") {
			return errors.New("invalid token file")
		}
	}
	client := &http.Client{Timeout: o.timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	base := strings.TrimSuffix(o.base, "/")
	check := func(name, path string, status int, auth bool, validate func([]byte) bool) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
		if err != nil {
			return errors.New("cannot construct smoke request")
		}
		if auth {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		started := time.Now()
		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("%s: request failed or timed out", name)
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
		closeErr := resp.Body.Close()
		if readErr != nil || closeErr != nil || len(body) > 1<<20 {
			return fmt.Errorf("%s: invalid response size/read", name)
		}
		if resp.StatusCode != status {
			return fmt.Errorf("%s: HTTP %d, expected %d", name, resp.StatusCode, status)
		}
		if validate != nil && !validate(body) {
			return fmt.Errorf("%s: response contract or expected ID mismatch", name)
		}
		_, err = fmt.Fprintf(out, "PASS %s status=%d elapsed_ms=%d\n", name, status, time.Since(started).Milliseconds())
		return err
	}
	for _, p := range []struct{ path, key string }{{"/health", "status"}, {"/livez", "live"}, {"/readyz", "ready"}, {"/readyz/realtime", "ready"}} {
		validate := func(body []byte) bool {
			var v map[string]any
			if json.Unmarshal(body, &v) != nil {
				return false
			}
			if p.key == "status" {
				return v[p.key] == "ok"
			}
			return v[p.key] == true
		}
		if err := check(p.path, p.path, 200, false, validate); err != nil {
			return err
		}
	}
	if err := check("public-metrics-denied", "/metrics", 404, false, nil); err != nil {
		return err
	}
	if token == "" {
		return nil
	}
	if err := check("authenticated-profile", "/api/v1/users/me", 200, true, func(b []byte) bool {
		var profile struct {
			ID int64 `json:"id"`
		}
		return json.Unmarshal(b, &profile) == nil && profile.ID > 0
	}); err != nil {
		return err
	}
	if err := check("inbox-recovery", "/api/v1/users/me/notifications?limit=100", 200, true, collection("notifications", o.notification)); err != nil {
		return err
	}
	if o.room == 0 {
		return nil
	}
	path := "/api/v1/rooms/" + strconv.FormatInt(o.room, 10) + "/messages?limit=100"
	if err := check("history-recovery", path, 200, true, collection("messages", o.message)); err != nil {
		return err
	}
	for _, name := range []string{"ws-join", "ws-reconnect-rejoin"} {
		if err := join(ctx, u, o, token); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		if _, err := fmt.Fprintln(out, "PASS", name); err != nil {
			return err
		}
	}
	return nil
}

func collection(key string, expected int64) func([]byte) bool {
	return func(body []byte) bool {
		var v map[string]json.RawMessage
		if json.Unmarshal(body, &v) != nil || len(v[key]) == 0 || string(v[key]) == "null" {
			return false
		}
		var rows []struct {
			ID int64 `json:"id"`
		}
		if json.Unmarshal(v[key], &rows) != nil {
			return false
		}
		if expected == 0 {
			return true
		}
		for _, row := range rows {
			if row.ID == expected {
				return true
			}
		}
		return false
	}
}

func join(ctx context.Context, base *url.URL, o options, token string) error {
	u := *base
	u.Path = "/api/v1/ws"
	u.Scheme = "wss"
	if base.Scheme == "http" {
		u.Scheme = "ws"
	}
	dialer := websocket.Dialer{HandshakeTimeout: o.timeout}
	conn, resp, err := dialer.DialContext(ctx, u.String(), http.Header{"Authorization": {"Bearer " + token}, "Origin": {o.origin}})
	if resp != nil && resp.Body != nil {
		resp.Body.Close()
	}
	if err != nil {
		return errors.New("upgrade failed or timed out")
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	conn.SetReadLimit(64 << 10)
	deadline := time.Now().Add(min(o.timeout, 15*time.Second))
	if err := conn.SetWriteDeadline(deadline); err != nil {
		return errors.New("cannot set write deadline")
	}
	room := strconv.FormatInt(o.room, 10)
	if err := conn.WriteJSON(struct {
		Type string `json:"type"`
		Room string `json:"room_id"`
	}{"join", room}); err != nil {
		return errors.New("join write failed")
	}
	if err := conn.SetReadDeadline(deadline); err != nil {
		return errors.New("cannot set read deadline")
	}
	for count := 0; count < 64; {
		_, frame, err := conn.ReadMessage()
		if err != nil {
			return errors.New("join snapshot unavailable")
		}
		// The server can batch several newline-separated envelopes in one frame.
		decoder := json.NewDecoder(bytes.NewReader(frame))
		for count < 64 {
			var event struct {
				Type    string `json:"type"`
				Room    string `json:"room_id"`
				Payload struct {
					Room string `json:"room_id"`
				} `json:"payload"`
			}
			if err := decoder.Decode(&event); err != nil {
				if errors.Is(err, io.EOF) {
					break
				}
				return errors.New("invalid join event")
			}
			count++
			if event.Type == "error" {
				return errors.New("join rejected")
			}
			if event.Type == "presence_snapshot" && event.Room == room && event.Payload.Room == room {
				return conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), deadline)
			}
		}
	}
	return errors.New("join snapshot not found within event limit")
}
