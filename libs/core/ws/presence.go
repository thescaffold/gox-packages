package ws

import (
	"encoding/json"
	"sort"
	"time"
)

// Presence is one connection in a room.
type Presence struct {
	ClientID string `json:"clientId"`
	// UserID is Config.PresenceID of the connection's claims ("" when
	// anonymous). One user with two tabs has two entries.
	UserID string `json:"userId,omitempty"`
	// State is what the client last sent in a "presence" frame (typing,
	// cursor, status).
	State    json.RawMessage `json:"state,omitempty"`
	JoinedAt time.Time       `json:"joinedAt"`
	// Node is the hub the connection is on.
	Node string `json:"node,omitempty"`

	namespace, room string
}

const (
	kindEvent        = "event"
	kindPresence     = "presence"
	kindPresenceSync = "presence-sync"
)

// envelope is what hubs exchange through the Adapter.
type envelope struct {
	Node      string          `json:"node"`
	Kind      string          `json:"kind"`
	Namespace string          `json:"namespace,omitempty"`
	Room      string          `json:"room,omitempty"`
	Event     string          `json:"event,omitempty"`
	Payload   json.RawMessage `json:"payload,omitempty"`
	Except    string          `json:"except,omitempty"`
	From      string          `json:"from,omitempty"`
	Presence  *Presence       `json:"presence,omitempty"`
	Snapshot  []roomMembers   `json:"snapshot,omitempty"`
}

type roomMembers struct {
	Namespace string     `json:"namespace"`
	Room      string     `json:"room"`
	Members   []Presence `json:"members"`
}

type nodePresence struct {
	seen  time.Time
	rooms map[string]*roomMembers // room key → members
}

// Presence lists who is in namespace/room, on every node (with an Adapter),
// oldest first. Empty unless Config.Presence.
func (s *WsService) Presence(namespace, room string) []Presence {
	key := roomKey(namespace, room)
	var out []Presence
	s.mu.RLock()
	for _, p := range s.local[key] {
		out = append(out, *p)
	}
	s.mu.RUnlock()
	s.pmu.Lock()
	for _, n := range s.remote {
		if rm := n.rooms[key]; rm != nil {
			out = append(out, rm.Members...)
		}
	}
	s.pmu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		if !out[i].JoinedAt.Equal(out[j].JoinedAt) {
			return out[i].JoinedAt.Before(out[j].JoinedAt)
		}
		return out[i].ClientID < out[j].ClientID
	})
	if out == nil {
		out = []Presence{}
	}
	return out
}

// addLocalPresence records c in a room. Caller holds s.mu.
func (s *WsService) addLocalPresence(c *Client, namespace, room string) *Presence {
	key := roomKey(namespace, room)
	if s.local[key] == nil {
		s.local[key] = map[string]*Presence{}
	}
	p := &Presence{ClientID: c.id, UserID: s.cfg.PresenceID(c.Claims()), JoinedAt: time.Now().UTC(), Node: s.node, namespace: namespace, room: room}
	s.local[key][c.id] = p
	return p
}

// removeLocalPresence drops c from a room. Caller holds s.mu.
func (s *WsService) removeLocalPresence(c *Client, key string) *Presence {
	p := s.local[key][c.id]
	if p == nil {
		return nil
	}
	delete(s.local[key], c.id)
	if len(s.local[key]) == 0 {
		delete(s.local, key)
	}
	return p
}

// handlePresenceUpdate sets a client's presence state in a room it joined.
func (s *WsService) handlePresenceUpdate(c *Client, f inFrame) {
	if !s.cfg.Presence {
		s.sendErrorRef(c, "presence is not enabled", f.ID)
		return
	}
	if !s.member(c, f.Namespace, f.Room) {
		s.sendErrorRef(c, "not a member of this room", f.ID)
		return
	}
	if c.bucket != nil && !c.bucket.allow() {
		s.sendErrorRef(c, "rate limited", f.ID)
		return
	}
	if s.cfg.AuthorizeMessage != nil {
		m := Message{ID: f.ID, Namespace: f.Namespace, Room: f.Room, Event: "presence.update", Payload: []byte(f.Payload)}
		if err := s.cfg.AuthorizeMessage(c, m); err != nil {
			s.sendErrorRef(c, clientText(err, "forbidden"), f.ID)
			return
		}
	}
	key := roomKey(f.Namespace, f.Room)
	s.mu.Lock()
	p := s.local[key][c.id]
	var snapshot Presence
	if p != nil {
		if len(f.Payload) > 0 {
			p.State = append(json.RawMessage(nil), f.Payload...)
		} else {
			p.State = nil
		}
		snapshot = *p
	}
	s.mu.Unlock()
	if p == nil {
		s.sendErrorRef(c, "not a member of this room", f.ID)
		return
	}
	s.announce(f.Namespace, f.Room, "update", snapshot, c.id)
	if f.ID != "" {
		s.sendFrame(c, outFrame{Type: "ack", Namespace: f.Namespace, Room: f.Room, ID: f.ID})
	}
}

// announce tells this node's room members, and the other nodes, about a
// presence change. Never call it while holding s.mu.
func (s *WsService) announce(namespace, room, event string, p Presence, except string) {
	s.announceLocal(namespace, room, event, p, except)
	if s.cfg.Adapter != nil {
		pp := p
		_ = s.publish(envelope{Kind: kindPresence, Namespace: namespace, Room: room, Event: event, Presence: &pp})
	}
}

func (s *WsService) announceLocal(namespace, room, event string, p Presence, except string) {
	data, err := json.Marshal(outFrame{Type: "presence", Namespace: namespace, Room: room, Event: event, Payload: p})
	if err != nil {
		return
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for c := range s.rooms[roomKey(namespace, room)] {
		if c.id != except {
			s.enqueue(c, data)
		}
	}
}

// remotePresence applies another node's join, update or leave.
func (s *WsService) remotePresence(env envelope) {
	if env.Presence == nil || !s.cfg.Presence {
		return
	}
	key := roomKey(env.Namespace, env.Room)
	p := *env.Presence
	p.Node = env.Node
	s.pmu.Lock()
	n := s.remote[env.Node]
	if n == nil {
		n = &nodePresence{rooms: map[string]*roomMembers{}}
		s.remote[env.Node] = n
	}
	n.seen = time.Now()
	rm := n.rooms[key]
	if rm == nil {
		rm = &roomMembers{Namespace: env.Namespace, Room: env.Room}
		n.rooms[key] = rm
	}
	kept := rm.Members[:0]
	for _, m := range rm.Members {
		if m.ClientID != p.ClientID {
			kept = append(kept, m)
		}
	}
	rm.Members = kept
	if env.Event != "leave" {
		rm.Members = append(rm.Members, p)
	}
	if len(rm.Members) == 0 {
		delete(n.rooms, key)
	}
	s.pmu.Unlock()
	s.announceLocal(env.Namespace, env.Room, env.Event, p, "")
}

// remoteSync replaces another node's presence with its snapshot, telling this
// node's members what changed. An empty snapshot means the node left.
func (s *WsService) remoteSync(env envelope) {
	if !s.cfg.Presence {
		return
	}
	next := map[string]*roomMembers{}
	for i := range env.Snapshot {
		rm := env.Snapshot[i]
		for j := range rm.Members {
			rm.Members[j].Node = env.Node
		}
		next[roomKey(rm.Namespace, rm.Room)] = &rm
	}
	s.pmu.Lock()
	var before map[string]*roomMembers
	if n := s.remote[env.Node]; n != nil {
		before = n.rooms
	}
	if len(next) == 0 {
		delete(s.remote, env.Node)
	} else {
		s.remote[env.Node] = &nodePresence{seen: time.Now(), rooms: next}
	}
	s.pmu.Unlock()
	s.announceDiff(before, next)
}

// announceDiff sends join and leave frames for the difference between two
// views of one node's presence.
func (s *WsService) announceDiff(before, after map[string]*roomMembers) {
	index := func(m map[string]*roomMembers) map[string]map[string]Presence {
		out := map[string]map[string]Presence{}
		for k, rm := range m {
			out[k] = map[string]Presence{}
			for _, p := range rm.Members {
				out[k][p.ClientID] = p
			}
		}
		return out
	}
	b, a := index(before), index(after)
	for k, members := range b {
		for id, p := range members {
			if _, ok := a[k][id]; !ok {
				rm := before[k]
				s.announceLocal(rm.Namespace, rm.Room, "leave", p, "")
			}
		}
	}
	for k, members := range a {
		for id, p := range members {
			if _, ok := b[k][id]; !ok {
				rm := after[k]
				s.announceLocal(rm.Namespace, rm.Room, "join", p, "")
			}
		}
	}
}

// heartbeat republishes this node's presence and drops silent nodes.
func (s *WsService) heartbeat() {
	t := time.NewTicker(s.cfg.PresenceHeartbeat)
	defer t.Stop()
	for {
		select {
		case <-s.stopBeat:
			return
		case <-t.C:
			s.publishSnapshot()
			s.sweep(time.Now().Add(-3 * s.cfg.PresenceHeartbeat))
		}
	}
}

func (s *WsService) publishSnapshot() {
	s.mu.RLock()
	snap := make([]roomMembers, 0, len(s.local))
	for _, set := range s.local {
		var rm roomMembers
		for _, p := range set {
			rm.Namespace, rm.Room = p.namespace, p.room
			rm.Members = append(rm.Members, *p)
		}
		if len(rm.Members) > 0 {
			snap = append(snap, rm)
		}
	}
	s.mu.RUnlock()
	// An empty snapshot reads as "this node has no one", which is the truth.
	_ = s.publish(envelope{Kind: kindPresenceSync, Snapshot: snap})
}

// sweep drops nodes not heard from since cutoff.
func (s *WsService) sweep(cutoff time.Time) {
	s.pmu.Lock()
	var dead []map[string]*roomMembers
	for node, n := range s.remote {
		if n.seen.Before(cutoff) {
			dead = append(dead, n.rooms)
			delete(s.remote, node)
		}
	}
	s.pmu.Unlock()
	for _, rooms := range dead {
		s.announceDiff(rooms, nil)
	}
}
