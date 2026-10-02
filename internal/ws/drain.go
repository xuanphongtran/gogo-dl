package ws

import "context"

// BeginDrain synchronizes with pump creation and rejects future upgrades/admissions.
// It does not access event-loop-owned state or stop persisted request completion.
func (h *Hub) BeginDrain() {
	h.lifecycleMu.Lock()
	h.draining.Store(true)
	h.lifecycleMu.Unlock()
}

// Ready reports process-local realtime admission state without inspecting Hub maps.
func (h *Hub) Ready() bool { return h.running.Load() && !h.draining.Load() }

// Drain stops the loop, sends 1001 through each sole writer and waits for owned
// pumps/upgrades/authorization work. A deadline forcibly closes remaining sockets.
// Run must be started; cleanup also handles its goroutine being scheduled late.
func (h *Hub) Drain(ctx context.Context) error {
	h.Shutdown()
	finished := make(chan struct{})
	go func() {
		// The loop publishes an immutable socket snapshot before stopped closes.
		// Arm force-close even if the budget expired before the loop was scheduled.
		<-h.stopped
		stopForce := context.AfterFunc(ctx, h.forceClosedClients)
		defer stopForce()
		h.upgrades.Wait()
		h.pumps.Wait()
		h.authorizations.Wait()
		close(finished)
	}()
	select {
	case <-finished:
		return nil
	case <-ctx.Done():
		// Complete force-close before returning when the snapshot is already safe.
		select {
		case <-h.stopped:
			h.forceClosedClients()
		default:
		}
		return ctx.Err()
	}
}

func (h *Hub) forceClosedClients() {
	for _, c := range h.closedClients {
		if c.conn != nil && !c.writerFinished.Load() {
			if c.drainOutcome.CompareAndSwap(0, 2) {
				h.metrics.WSDrain("forced")
			}
			_ = c.conn.Close()
		}
	}
}

func (h *Hub) observeQueues() {
	if h.metrics == nil {
		return
	}
	queued := 0
	for _, c := range h.clients {
		queued += len(c.send)
	}
	h.metrics.WSQueues(len(h.clients), len(h.inbound), len(h.broadcast), len(h.userBroadcast), queued)
}
