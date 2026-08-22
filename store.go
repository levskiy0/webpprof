package webpprof

import (
	"slices"
	"strings"
	"sync"
	"time"
)

type streamMessage struct {
	Type    string              `json:"type"`
	Cursor  uint64              `json:"cursor,omitempty"`
	Event   *Entry              `json:"event,omitempty"`
	Runtime *RuntimeStats       `json:"runtime,omitempty"`
	Queues  *QueueStatsResponse `json:"queues,omitempty"`
	Dropped uint64              `json:"dropped,omitempty"`
}

type entryStore struct {
	mu             sync.RWMutex
	entries        map[string]Entry
	order          []string
	bytes          int64
	nextCursor     uint64
	dropped        uint64
	retention      time.Duration
	maxEvents      int
	maxBytes       int64
	streamBuffer   int
	subscribers    map[uint64]chan streamMessage
	nextSubscriber uint64
	isClosed       bool
}

func newEntryStore(c config) *entryStore {
	return &entryStore{
		entries:      make(map[string]Entry),
		order:        make([]string, 0, c.maxEvents),
		retention:    c.retention,
		maxEvents:    c.maxEvents,
		maxBytes:     c.maxBytes,
		streamBuffer: c.streamBuffer,
		subscribers:  make(map[uint64]chan streamMessage),
	}
}

func (s *entryStore) put(entry Entry) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.isClosed {
		return false
	}
	s.purgeExpiredLocked(time.Now())
	size := entrySize(entry)
	if size > s.maxBytes {
		s.dropped++
		s.broadcastLocked(streamMessage{Type: "events.dropped", Dropped: s.dropped})
		return false
	}
	messageType := "event.created"
	if previous, ok := s.entries[entry.ID]; ok {
		s.bytes -= entrySize(previous)
		s.removeOrderLocked(entry.ID)
		messageType = "event.updated"
	}
	s.nextCursor++
	entry.Cursor = s.nextCursor
	entry.Data = slices.Clone(entry.Data)
	entry.Tags = cloneTags(entry.Tags)
	s.entries[entry.ID] = entry
	s.order = append(s.order, entry.ID)
	s.bytes += size
	s.evictLocked()
	copy := cloneEntry(entry)
	s.broadcastLocked(streamMessage{Type: messageType, Cursor: entry.Cursor, Event: &copy})
	return true
}

func (s *entryStore) list(kind Kind, requestID string, tags []string, after uint64, limit int) []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.purgeExpiredLocked(time.Now())
	if limit <= 0 || limit > 1_000 {
		limit = 200
	}
	result := make([]Entry, 0, min(limit, len(s.order)))
	for index := len(s.order) - 1; index >= 0 && len(result) < limit; index-- {
		entry := s.entries[s.order[index]]
		if after > 0 && entry.Cursor <= after {
			continue
		}
		if kind != "" && entry.Kind != kind {
			continue
		}
		if requestID != "" && entry.RequestID != requestID && entry.ID != requestID && entry.OriginRequestID != requestID {
			continue
		}
		if !matchesTags(entry.Tags, tags) {
			continue
		}
		result = append(result, cloneEntry(entry))
	}
	slices.Reverse(result)
	return result
}

func matchesTags(tags map[string]string, filters []string) bool {
	for _, filter := range filters {
		filter = strings.TrimSpace(filter)
		if filter == "" {
			continue
		}
		key, value, exact := strings.Cut(filter, "=")
		key = strings.TrimSpace(key)
		if key == "" {
			return false
		}
		actual, exists := tags[key]
		if !exists || exact && actual != strings.TrimSpace(value) {
			return false
		}
	}
	return true
}

func (s *entryStore) get(id string) (Entry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.purgeExpiredLocked(time.Now())
	entry, ok := s.entries[id]
	return cloneEntry(entry), ok
}

func (s *entryStore) clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = make(map[string]Entry)
	s.order = s.order[:0]
	s.bytes = 0
	s.nextCursor++
	s.broadcastLocked(streamMessage{Type: "events.cleared", Cursor: s.nextCursor})
}

func (s *entryStore) stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.purgeExpiredLocked(time.Now())
	return Stats{Events: len(s.entries), Bytes: s.bytes, DroppedEvents: s.dropped, Subscribers: len(s.subscribers), Cursor: s.nextCursor}
}

func (s *entryStore) subscribe() (<-chan streamMessage, func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.isClosed {
		closed := make(chan streamMessage)
		close(closed)
		return closed, func() {}
	}
	s.nextSubscriber++
	id := s.nextSubscriber
	updates := make(chan streamMessage, s.streamBuffer)
	s.subscribers[id] = updates
	var once sync.Once
	return updates, func() {
		once.Do(func() {
			s.mu.Lock()
			if channel, ok := s.subscribers[id]; ok {
				delete(s.subscribers, id)
				close(channel)
			}
			s.mu.Unlock()
		})
	}
}

func (s *entryStore) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.isClosed {
		return
	}
	s.isClosed = true
	for id, subscriber := range s.subscribers {
		delete(s.subscribers, id)
		close(subscriber)
	}
}

func (s *entryStore) purgeExpiredLocked(now time.Time) {
	cutoff := now.Add(-s.retention)
	for len(s.order) > 0 {
		id := s.order[0]
		entry := s.entries[id]
		if !entry.RecordedAt.Before(cutoff) {
			break
		}
		s.removeLocked(id)
	}
}

func (s *entryStore) evictLocked() {
	for len(s.order) > s.maxEvents || s.bytes > s.maxBytes {
		if len(s.order) == 0 {
			return
		}
		s.removeLocked(s.order[0])
	}
}

func (s *entryStore) removeLocked(id string) {
	entry, ok := s.entries[id]
	if !ok {
		return
	}
	delete(s.entries, id)
	s.bytes -= entrySize(entry)
	s.removeOrderLocked(id)
}

func (s *entryStore) removeOrderLocked(id string) {
	for index, current := range s.order {
		if current == id {
			s.order = append(s.order[:index], s.order[index+1:]...)
			return
		}
	}
}

func (s *entryStore) broadcastLocked(message streamMessage) {
	for id, subscriber := range s.subscribers {
		select {
		case subscriber <- message:
		default:
			delete(s.subscribers, id)
			close(subscriber)
		}
	}
}

func entrySize(entry Entry) int64 {
	return int64(len(entry.ID)+len(entry.RequestID)+len(entry.ParentID)+len(entry.OriginRequestID)+len(entry.Process)+len(entry.Instance)+len(entry.Kind)+len(entry.Data)) + 160
}

func cloneEntry(entry Entry) Entry {
	entry.Data = slices.Clone(entry.Data)
	entry.Tags = cloneTags(entry.Tags)
	return entry
}

func cloneTags(tags map[string]string) map[string]string {
	if len(tags) == 0 {
		return nil
	}
	copy := make(map[string]string, len(tags))
	for key, value := range tags {
		copy[key] = value
	}
	return copy
}
