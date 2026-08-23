package webpprof

import (
	"context"
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
	order          entryOrder
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
	storageKind    storageKind
	storagePath    string
	storageFile    *os.File
	storage        EntryStorage
	storageBytes   int64
	storageError   string
	bodyLimit      int64
	requestSample  float64
	disabledKinds  []Kind
	storageMu      sync.Mutex
	storageCond    *sync.Cond
	storageIssued  uint64
	storageDone    uint64
}

type storeJournalRecord struct {
	Operation string `json:"operation"`
	Entry     *Entry `json:"entry,omitempty"`
	Cursor    uint64 `json:"cursor,omitempty"`
}

type storageBatch struct {
	sequence uint64
	records  []storeJournalRecord
}

func newEntryStore(c config) *entryStore {
	disabledKinds := make([]Kind, 0, len(c.disabledKinds))
	for kind := range c.disabledKinds {
		disabledKinds = append(disabledKinds, kind)
	}
	slices.Sort(disabledKinds)
	store := &entryStore{
		entries:       make(map[string]Entry),
		order:         newEntryOrder(c.maxEvents),
		retention:     c.retention,
		maxEvents:     c.maxEvents,
		maxBytes:      c.maxBytes,
		streamBuffer:  c.streamBuffer,
		subscribers:   make(map[uint64]chan streamMessage),
		storageKind:   c.storageKind,
		storagePath:   c.storagePath,
		storage:       c.storage,
		bodyLimit:     c.bodyLimit,
		requestSample: c.requestSample,
		disabledKinds: disabledKinds,
	}
	store.storageCond = sync.NewCond(&store.storageMu)
	store.openStorage()
	return store
}

func (s *entryStore) put(entry Entry) bool {
	s.mu.Lock()
	if s.isClosed {
		s.mu.Unlock()
		return false
	}
	records := s.purgeExpiredLocked(time.Now())
	size := entrySize(entry)
	if size > s.maxBytes {
		s.dropped++
		s.broadcastLocked(streamMessage{Type: "events.dropped", Dropped: s.dropped})
		batch := s.storageBatchLocked(records)
		s.mu.Unlock()
		s.persist(batch)
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
	s.order.pushBack(entry.ID)
	s.bytes += size
	records = append(records, s.evictLocked()...)
	records = append(records, storeJournalRecord{Operation: "put", Entry: &entry, Cursor: s.nextCursor})
	copy := cloneEntry(entry)
	s.broadcastLocked(streamMessage{Type: messageType, Cursor: entry.Cursor, Event: &copy})
	batch := s.storageBatchLocked(records)
	s.mu.Unlock()
	s.persist(batch)
	return true
}

func (s *entryStore) list(kind Kind, requestID string, tags []string, after uint64, limit int) []Entry {
	return s.listBefore(kind, requestID, tags, after, 0, limit)
}

func (s *entryStore) listBefore(kind Kind, requestID string, tags []string, after, before uint64, limit int) []Entry {
	entries, _ := s.listBeforeFiltered(eventFilters{Kind: kind, RequestID: requestID, Tags: tags, After: after, Before: before}, limit)
	return entries
}

type eventFilters struct {
	Kind          Kind
	RequestID     string
	Tags          []string
	Query         string
	Method        string
	PathContains  string
	Status        int
	MinDurationNS int64
	MaxDurationNS int64
	After         uint64
	Before        uint64
}

func (s *entryStore) listBeforeFiltered(filters eventFilters, limit int) ([]Entry, int) {
	s.mu.Lock()
	records := s.purgeExpiredLocked(time.Now())
	if limit <= 0 || limit > 1_001 {
		limit = 200
	}
	result := make([]Entry, 0, min(limit, s.order.len()))
	scanned := 0
	for index := s.order.len() - 1; index >= 0 && len(result) < limit; index-- {
		entry := s.entries[s.order.at(index)]
		if filters.After > 0 && entry.Cursor <= filters.After {
			continue
		}
		if filters.Before > 0 && entry.Cursor >= filters.Before {
			continue
		}
		scanned++
		if !matchesEventFilters(entry, filters) {
			continue
		}
		result = append(result, cloneEntry(entry))
	}
	slices.Reverse(result)
	batch := s.storageBatchLocked(records)
	s.mu.Unlock()
	s.persist(batch)
	return result, scanned
}

func matchesEventFilters(entry Entry, filters eventFilters) bool {
	if filters.Kind != "" && entry.Kind != filters.Kind {
		return false
	}
	if filters.RequestID != "" && entry.RequestID != filters.RequestID && entry.ID != filters.RequestID && entry.OriginRequestID != filters.RequestID {
		return false
	}
	if !matchesTags(entry.Tags, filters.Tags) {
		return false
	}
	if filters.MinDurationNS > 0 && entry.DurationNS < filters.MinDurationNS {
		return false
	}
	if filters.MaxDurationNS > 0 && entry.DurationNS > filters.MaxDurationNS {
		return false
	}
	query := strings.ToLower(strings.TrimSpace(filters.Query))
	if query != "" && !entryContains(entry, query) {
		return false
	}
	method := strings.TrimSpace(filters.Method)
	pathContains := strings.ToLower(strings.TrimSpace(filters.PathContains))
	if method == "" && pathContains == "" && filters.Status == 0 {
		return true
	}
	if entry.Kind != KindRequest {
		return false
	}
	var request struct {
		Method string `json:"method"`
		Path   string `json:"path"`
		Status int    `json:"status"`
	}
	if json.Unmarshal(entry.Data, &request) != nil {
		return false
	}
	if method != "" && !strings.EqualFold(method, request.Method) {
		return false
	}
	if pathContains != "" && !strings.Contains(strings.ToLower(request.Path), pathContains) {
		return false
	}
	return filters.Status == 0 || filters.Status == request.Status
}

func entryContains(entry Entry, query string) bool {
	if strings.Contains(strings.ToLower(entry.ID), query) || strings.Contains(strings.ToLower(string(entry.Kind)), query) || strings.Contains(strings.ToLower(entry.RequestID), query) || strings.Contains(strings.ToLower(entry.Process), query) || strings.Contains(strings.ToLower(entry.Instance), query) || strings.Contains(strings.ToLower(string(entry.Data)), query) {
		return true
	}
	for key, value := range entry.Tags {
		if strings.Contains(strings.ToLower(key), query) || strings.Contains(strings.ToLower(value), query) {
			return true
		}
	}
	return false
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
	records := s.purgeExpiredLocked(time.Now())
	entry, ok := s.entries[id]
	result := cloneEntry(entry)
	batch := s.storageBatchLocked(records)
	s.mu.Unlock()
	s.persist(batch)
	return result, ok
}

func (s *entryStore) requestEntries(requestID string) (Entry, []Entry, bool) {
	s.mu.Lock()
	records := s.purgeExpiredLocked(time.Now())
	request, ok := s.entries[requestID]
	if !ok {
		batch := s.storageBatchLocked(records)
		s.mu.Unlock()
		s.persist(batch)
		return Entry{}, nil, false
	}
	entries := make([]Entry, 0)
	for index := 0; index < s.order.len(); index++ {
		id := s.order.at(index)
		entry := s.entries[id]
		if entry.ID != requestID && entry.RequestID != requestID && entry.OriginRequestID != requestID {
			continue
		}
		entries = append(entries, cloneEntry(entry))
	}
	result := cloneEntry(request)
	batch := s.storageBatchLocked(records)
	s.mu.Unlock()
	s.persist(batch)
	return result, entries, true
}

func (s *entryStore) clear() {
	s.mu.Lock()
	s.entries = make(map[string]Entry)
	s.order.reset()
	s.bytes = 0
	s.nextCursor++
	s.broadcastLocked(streamMessage{Type: "events.cleared", Cursor: s.nextCursor})
	batch := s.storageBatchLocked([]storeJournalRecord{{Operation: "clear", Cursor: s.nextCursor}})
	s.mu.Unlock()
	s.persist(batch)
}

func (s *entryStore) stats() Stats {
	s.mu.Lock()
	records := s.purgeExpiredLocked(time.Now())
	storage := string(s.storageKind)
	stats := Stats{Events: len(s.entries), Bytes: s.bytes, DroppedEvents: s.dropped, EvictedEvents: s.evicted, Subscribers: len(s.subscribers), Cursor: s.nextCursor, MaxEvents: s.maxEvents, MaxBytes: s.maxBytes, RetentionNS: int64(s.retention), Storage: storage, StorageError: s.storageError, BodyLimit: s.bodyLimit, SampleRate: s.requestSample, DisabledKinds: slices.Clone(s.disabledKinds)}
	batch := s.storageBatchLocked(records)
	s.mu.Unlock()
	s.persist(batch)
	return stats
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
	if s.isClosed {
		s.mu.Unlock()
		return
	}
	s.isClosed = true
	issued := s.storageIssued
	for id, subscriber := range s.subscribers {
		delete(s.subscribers, id)
		close(subscriber)
	}
	s.mu.Unlock()

	s.storageMu.Lock()
	for s.storageDone < issued {
		s.storageCond.Wait()
	}
	var storageErr error
	if s.storageFile != nil {
		if err := s.storageFile.Sync(); err != nil {
			storageErr = errors.Join(storageErr, err)
		}
		if err := s.storageFile.Close(); err != nil {
			storageErr = errors.Join(storageErr, err)
		}
		s.storageFile = nil
	}
	if s.storage != nil {
		if err := s.storage.Close(); err != nil {
			storageErr = errors.Join(storageErr, err)
		}
		s.storage = nil
	}
	s.storageMu.Unlock()
	s.setStorageError(storageErr)
}

func (s *entryStore) purgeExpiredLocked(now time.Time) []storeJournalRecord {
	var records []storeJournalRecord
	cutoff := now.Add(-s.retention)
	for s.order.len() > 0 {
		id, _ := s.order.front()
		entry := s.entries[id]
		if !entry.RecordedAt.Before(cutoff) {
			break
		}
		if record, ok := s.removeOldestLocked(); ok {
			records = append(records, record)
		}
		s.evicted++
	}
	return records
}

func (s *entryStore) evictLocked() []storeJournalRecord {
	var records []storeJournalRecord
	for s.order.len() > s.maxEvents || s.bytes > s.maxBytes {
		if s.order.len() == 0 {
			break
		}
		if record, ok := s.removeOldestLocked(); ok {
			records = append(records, record)
		}
		s.evicted++
	}
	return records
}

func (s *entryStore) openStorage() {
	if s.storageKind == storageKindMemory {
		return
	}
	if s.storage != nil {
		s.openExternalStorage()
		return
	}
	if s.storagePath == "" {
		return
	}
	s.openJournalStorage()
}

func (s *entryStore) openExternalStorage() {
	entries, cursor, err := s.storage.Load(context.Background())
	for _, entry := range entries {
		copy := entry
		s.replayRecord(storeJournalRecord{Operation: "put", Entry: &copy})
	}
	if cursor > s.nextCursor {
		s.nextCursor = cursor
	}
	if err != nil {
		s.storageError = err.Error()
	}
	records := s.purgeExpiredLocked(time.Now())
	records = append(records, s.evictLocked()...)
	for _, record := range records {
		if persistErr := s.persistRecord(record); persistErr != nil {
			s.storageError = persistErr.Error()
		}
	}
}

func (s *entryStore) openJournalStorage() {
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
		s.order.reset()
		s.bytes = 0
		return
	}
	if record.Operation == "delete" && record.Entry != nil {
		s.removeMemoryLocked(record.Entry.ID)
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
	s.order.pushBack(entry.ID)
	s.bytes += entrySize(entry)
	if entry.Cursor > s.nextCursor {
		s.nextCursor = entry.Cursor
	}
}

func (s *entryStore) persistRecord(record storeJournalRecord) error {
	switch s.storageKind {
	case storageKindJournal:
		if err := s.appendJournal(record); err != nil {
			return err
		}
		if record.Operation == "clear" {
			return s.compactJournal()
		}
	default:
		if s.storage == nil {
			return nil
		}
		switch record.Operation {
		case "put":
			if record.Entry != nil {
				return s.storage.Put(context.Background(), *record.Entry, record.Cursor)
			}
		case "delete":
			if record.Entry != nil {
				return s.storage.Delete(context.Background(), record.Entry.ID)
			}
		case "clear":
			return s.storage.Clear(context.Background(), record.Cursor)
		}
	}
	return nil
}

func (s *entryStore) appendJournal(record storeJournalRecord) error {
	if s.storageFile == nil {
		return nil
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	written, err := s.storageFile.Write(encoded)
	s.storageBytes += int64(written)
	if err != nil {
		return err
	}
	threshold := max(s.maxBytes*2, int64(1<<20))
	if s.storageBytes > threshold {
		return s.compactJournal()
	}
	return nil
}

func (s *entryStore) compactJournal() error {
	if s.storageFile == nil {
		return nil
	}
	s.mu.RLock()
	entries := make([]Entry, 0, s.order.len())
	for index := 0; index < s.order.len(); index++ {
		entries = append(entries, cloneEntry(s.entries[s.order.at(index)]))
	}
	s.mu.RUnlock()
	temporary := s.storagePath + ".tmp"
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(file)
	for index := range entries {
		entry := entries[index]
		if err := encoder.Encode(storeJournalRecord{Operation: "put", Entry: &entry}); err != nil {
			_ = file.Close()
			return err
		}
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := s.storageFile.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporary, s.storagePath); err != nil {
		return err
	}
	s.storageFile, err = os.OpenFile(s.storagePath, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	info, err := s.storageFile.Stat()
	if err == nil {
		s.storageBytes = info.Size()
	}
	return nil
}

func (s *entryStore) removeLocked(id string) {
	if _, ok := s.entries[id]; !ok {
		return
	}
	s.removeMemoryLocked(id)
}

func (s *entryStore) removeOldestLocked() (storeJournalRecord, bool) {
	id, ok := s.order.popFront()
	if !ok {
		return storeJournalRecord{}, false
	}
	entry, ok := s.entries[id]
	if !ok {
		return storeJournalRecord{}, false
	}
	delete(s.entries, id)
	s.bytes -= entrySize(entry)
	return storeJournalRecord{Operation: "delete", Entry: &Entry{ID: id}}, true
}

func (s *entryStore) removeMemoryLocked(id string) {
	entry, ok := s.entries[id]
	if !ok {
		return
	}
	delete(s.entries, id)
	s.bytes -= entrySize(entry)
	s.removeOrderLocked(id)
}

func (s *entryStore) removeOrderLocked(id string) {
	s.order.remove(id)
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

func (s *entryStore) storageBatchLocked(records []storeJournalRecord) storageBatch {
	if len(records) == 0 {
		return storageBatch{}
	}
	if s.storageKind == storageKindMemory {
		return storageBatch{}
	}
	if s.storageKind == storageKindJournal && s.storageFile == nil {
		return storageBatch{}
	}
	if s.storageKind != storageKindJournal && s.storage == nil {
		return storageBatch{}
	}
	s.storageIssued++
	return storageBatch{sequence: s.storageIssued, records: records}
}

// persist serializes storage batches by the order assigned under entryStore.mu,
// but performs all encoding and I/O after releasing that mutex.
func (s *entryStore) persist(batch storageBatch) {
	if batch.sequence == 0 {
		return
	}
	s.storageMu.Lock()
	for batch.sequence != s.storageDone+1 {
		s.storageCond.Wait()
	}
	var persistErr error
	for _, record := range batch.records {
		if err := s.persistRecord(record); err != nil {
			persistErr = errors.Join(persistErr, err)
		}
	}
	s.storageDone = batch.sequence
	s.storageCond.Broadcast()
	s.storageMu.Unlock()
	s.setStorageError(persistErr)
}

func (s *entryStore) setStorageError(err error) {
	if err == nil {
		return
	}
	s.mu.Lock()
	if s.storageError == "" {
		s.storageError = err.Error()
	}
	s.mu.Unlock()
}

func entrySize(entry Entry) int64 {
	size := int64(len(entry.ID)+len(entry.RequestID)+len(entry.ParentID)+len(entry.OriginRequestID)+len(entry.Process)+len(entry.Instance)+len(entry.Kind)+len(entry.Data)) + 160
	for key, value := range entry.Tags {
		size += int64(len(key) + len(value) + 32)
	}
	return size
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
