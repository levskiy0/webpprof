package webpprof

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

type streamMessage struct {
	Type      string              `json:"type"`
	Cursor    uint64              `json:"cursor,omitempty"`
	Event     *Entry              `json:"event,omitempty"`
	Stats     *Stats              `json:"stats,omitempty"`
	Runtime   *RuntimeStats       `json:"runtime,omitempty"`
	Queues    *QueueStatsResponse `json:"queues,omitempty"`
	Dashboard *DashboardSnapshot  `json:"dashboard,omitempty"`
	Dropped   uint64              `json:"dropped,omitempty"`
}

type entryStore struct {
	mu             sync.RWMutex
	entries        map[string]Entry
	order          []string
	bytes          int64
	nextCursor     uint64
	dropped        uint64
	evicted        uint64
	retention      time.Duration
	maxEvents      int
	maxBytes       int64
	streamBuffer   int
	subscribers    map[uint64]chan streamMessage
	nextSubscriber uint64
	isClosed       bool
	storagePath    string
	storageFile    *os.File
	storageBytes   int64
	storageError   string
	bodyLimit      int64
	requestSample  float64
	disabledKinds  []Kind
}

type storeJournalRecord struct {
	Operation string `json:"operation"`
	Entry     *Entry `json:"entry,omitempty"`
}

func newEntryStore(c config) *entryStore {
	disabledKinds := make([]Kind, 0, len(c.disabledKinds))
	for kind := range c.disabledKinds {
		disabledKinds = append(disabledKinds, kind)
	}
	slices.Sort(disabledKinds)
	store := &entryStore{
		entries:       make(map[string]Entry),
		order:         make([]string, 0, c.maxEvents),
		retention:     c.retention,
		maxEvents:     c.maxEvents,
		maxBytes:      c.maxBytes,
		streamBuffer:  c.streamBuffer,
		subscribers:   make(map[uint64]chan streamMessage),
		storagePath:   c.storagePath,
		bodyLimit:     c.bodyLimit,
		requestSample: c.requestSample,
		disabledKinds: disabledKinds,
	}
	store.openStorage()
	return store
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
	s.appendJournalLocked(storeJournalRecord{Operation: "put", Entry: &entry})
	copy := cloneEntry(entry)
	s.broadcastLocked(streamMessage{Type: messageType, Cursor: entry.Cursor, Event: &copy})
	return true
}

func (s *entryStore) list(kind Kind, requestID string, tags []string, after uint64, limit int) []Entry {
	return s.listBefore(kind, requestID, tags, after, 0, limit)
}

func (s *entryStore) listBefore(kind Kind, requestID string, tags []string, after, before uint64, limit int) []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.purgeExpiredLocked(time.Now())
	if limit <= 0 || limit > 1_001 {
		limit = 200
	}
	result := make([]Entry, 0, min(limit, len(s.order)))
	for index := len(s.order) - 1; index >= 0 && len(result) < limit; index-- {
		entry := s.entries[s.order[index]]
		if after > 0 && entry.Cursor <= after {
			continue
		}
		if before > 0 && entry.Cursor >= before {
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

func (s *entryStore) requestEntries(requestID string) (Entry, []Entry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.purgeExpiredLocked(time.Now())
	request, ok := s.entries[requestID]
	if !ok {
		return Entry{}, nil, false
	}
	entries := make([]Entry, 0)
	for _, id := range s.order {
		entry := s.entries[id]
		if entry.ID != requestID && entry.RequestID != requestID && entry.OriginRequestID != requestID {
			continue
		}
		entries = append(entries, cloneEntry(entry))
	}
	return cloneEntry(request), entries, true
}

func (s *entryStore) clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = make(map[string]Entry)
	s.order = s.order[:0]
	s.bytes = 0
	s.nextCursor++
	s.appendJournalLocked(storeJournalRecord{Operation: "clear"})
	s.compactJournalLocked()
	s.broadcastLocked(streamMessage{Type: "events.cleared", Cursor: s.nextCursor})
}

func (s *entryStore) stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.purgeExpiredLocked(time.Now())
	storage := "memory"
	if s.storagePath != "" {
		storage = "disk"
	}
	return Stats{Events: len(s.entries), Bytes: s.bytes, DroppedEvents: s.dropped, EvictedEvents: s.evicted, Subscribers: len(s.subscribers), Cursor: s.nextCursor, MaxEvents: s.maxEvents, MaxBytes: s.maxBytes, RetentionNS: int64(s.retention), Storage: storage, StorageError: s.storageError, BodyLimit: s.bodyLimit, SampleRate: s.requestSample, DisabledKinds: slices.Clone(s.disabledKinds)}
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
	if s.storageFile != nil {
		if err := s.storageFile.Sync(); err != nil && s.storageError == "" {
			s.storageError = err.Error()
		}
		if err := s.storageFile.Close(); err != nil && s.storageError == "" {
			s.storageError = err.Error()
		}
		s.storageFile = nil
	}
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
		s.evicted++
	}
}

func (s *entryStore) evictLocked() {
	for len(s.order) > s.maxEvents || s.bytes > s.maxBytes {
		if len(s.order) == 0 {
			return
		}
		s.removeLocked(s.order[0])
		s.evicted++
	}
}

func (s *entryStore) openStorage() {
	if s.storagePath == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(s.storagePath), 0o700); err != nil {
		s.storageError = err.Error()
		return
	}
	file, err := os.OpenFile(s.storagePath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		s.storageError = err.Error()
		return
	}
	if err := file.Chmod(0o600); err != nil {
		s.storageError = err.Error()
		_ = file.Close()
		return
	}
	decoder := json.NewDecoder(file)
	var lastGoodOffset int64
	for {
		var record storeJournalRecord
		if err := decoder.Decode(&record); err != nil {
			if !errors.Is(err, io.EOF) {
				s.storageError = "could not fully replay storage journal: " + err.Error()
				if truncateErr := file.Truncate(lastGoodOffset); truncateErr != nil {
					s.storageError += "; could not repair journal: " + truncateErr.Error()
				}
			}
			break
		}
		s.replayRecord(record)
		lastGoodOffset = decoder.InputOffset()
	}
	s.purgeExpiredLocked(time.Now())
	s.evictLocked()
	position, err := file.Seek(0, io.SeekEnd)
	if err != nil {
		s.storageError = err.Error()
		_ = file.Close()
		return
	}
	s.storageFile = file
	s.storageBytes = position
}

func (s *entryStore) replayRecord(record storeJournalRecord) {
	if record.Operation == "clear" {
		s.entries = make(map[string]Entry)
		s.order = s.order[:0]
		s.bytes = 0
		return
	}
	if record.Operation != "put" || record.Entry == nil || record.Entry.ID == "" {
		return
	}
	entry := cloneEntry(*record.Entry)
	if previous, ok := s.entries[entry.ID]; ok {
		s.bytes -= entrySize(previous)
		s.removeOrderLocked(entry.ID)
	}
	s.entries[entry.ID] = entry
	s.order = append(s.order, entry.ID)
	s.bytes += entrySize(entry)
	if entry.Cursor > s.nextCursor {
		s.nextCursor = entry.Cursor
	}
}

func (s *entryStore) appendJournalLocked(record storeJournalRecord) {
	if s.storageFile == nil {
		return
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		s.storageError = err.Error()
		return
	}
	encoded = append(encoded, '\n')
	written, err := s.storageFile.Write(encoded)
	s.storageBytes += int64(written)
	if err != nil {
		s.storageError = err.Error()
		return
	}
	threshold := max(s.maxBytes*2, int64(1<<20))
	if s.storageBytes > threshold {
		s.compactJournalLocked()
	}
}

func (s *entryStore) compactJournalLocked() {
	if s.storageFile == nil {
		return
	}
	temporary := s.storagePath + ".tmp"
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		s.storageError = err.Error()
		return
	}
	encoder := json.NewEncoder(file)
	for _, id := range s.order {
		entry := cloneEntry(s.entries[id])
		if err := encoder.Encode(storeJournalRecord{Operation: "put", Entry: &entry}); err != nil {
			_ = file.Close()
			s.storageError = err.Error()
			return
		}
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		s.storageError = err.Error()
		return
	}
	if err := file.Close(); err != nil {
		s.storageError = err.Error()
		return
	}
	if err := s.storageFile.Close(); err != nil {
		s.storageError = err.Error()
		return
	}
	if err := os.Rename(temporary, s.storagePath); err != nil {
		s.storageError = err.Error()
		return
	}
	s.storageFile, err = os.OpenFile(s.storagePath, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		s.storageError = err.Error()
		return
	}
	info, err := s.storageFile.Stat()
	if err == nil {
		s.storageBytes = info.Size()
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
