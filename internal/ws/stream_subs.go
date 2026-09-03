package ws

import "sync"

type streamSubEntry struct {
	roomID string
	cb     func(Message)
}

type streamSubRegistry struct {
	mu    sync.Mutex
	// subs[roomID][subID] -> cb
	subs map[string]map[string]func(Message)
}

var streamSubs = &streamSubRegistry{
	subs: make(map[string]map[string]func(Message)),
}

func RegisterStreamSubscriber(subID, roomID string, cb func(Message)) {
	streamSubs.mu.Lock()
	defer streamSubs.mu.Unlock()
	if _, ok := streamSubs.subs[roomID]; !ok {
		streamSubs.subs[roomID] = make(map[string]func(Message))
	}
	streamSubs.subs[roomID][subID] = cb
}

func UnregisterStreamSubscriber(subID, roomID string) {
	streamSubs.mu.Lock()
	defer streamSubs.mu.Unlock()
	if room, ok := streamSubs.subs[roomID]; ok {
		delete(room, subID)
		if len(room) == 0 {
			delete(streamSubs.subs, roomID)
		}
	}
}

func dispatchStreamSubs(roomID string, msg Message) {
	streamSubs.mu.Lock()
	defer streamSubs.mu.Unlock()
	room, ok := streamSubs.subs[roomID]
	if !ok {
		return
	}
	for _, cb := range room {
		func(cb func(Message)) {
			defer func() { _ = recover() }()
			cb(msg)
		}(cb)
	}
}
