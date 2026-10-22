//go:build integration

package recovery_test

import (
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

// databaseProxy faults only this test process's DB traffic, including pooled
// connections. Joining accept before waiting prevents Add racing with Wait.
type databaseProxy struct {
	listener    net.Listener
	target      string
	mu          sync.Mutex
	available   bool
	connections map[net.Conn]struct{}
	accepted    chan struct{}
	workers     sync.WaitGroup
}

func newDatabaseProxy(t *testing.T, target string) *databaseProxy {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &databaseProxy{listener: l, target: target, available: true, connections: make(map[net.Conn]struct{}), accepted: make(chan struct{})}
	go func() {
		defer close(p.accepted)
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			p.workers.Add(1)
			go p.forward(conn)
		}
	}()
	t.Cleanup(func() {
		l.Close()
		<-p.accepted
		p.setAvailable(false)
		p.workers.Wait()
	})
	return p
}

func (p *databaseProxy) setAvailable(available bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.available = available
	if !available {
		for conn := range p.connections {
			conn.Close()
		}
	}
}

func (p *databaseProxy) forward(conn net.Conn) {
	defer p.workers.Done()
	defer conn.Close()
	p.mu.Lock()
	available := p.available
	p.mu.Unlock()
	if !available {
		return
	}
	upstream, err := net.DialTimeout("tcp", p.target, time.Second)
	if err != nil {
		return
	}
	defer upstream.Close()
	p.mu.Lock()
	if !p.available {
		p.mu.Unlock()
		return
	}
	p.connections[conn] = struct{}{}
	p.connections[upstream] = struct{}{}
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		delete(p.connections, conn)
		delete(p.connections, upstream)
		p.mu.Unlock()
	}()
	done := make(chan struct{}, 1)
	go func() { io.Copy(upstream, conn); upstream.Close(); conn.Close(); done <- struct{}{} }()
	io.Copy(conn, upstream)
	conn.Close()
	upstream.Close()
	<-done
}
