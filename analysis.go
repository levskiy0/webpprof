package webpprof

import (
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"
)

const (
	analysisNPlusOneMinimum       = 3
	analysisSQLShareMinimum       = 50
	analysisSequentialHTTPMinimum = 3
	analysisSequentialHTTPMaxGap  = 100 * time.Millisecond
	analysisCacheBurstMinimum     = 3
	analysisCacheQueryMaxGap      = 100 * time.Millisecond
	analysisCacheMissRateMinimum  = 5
	analysisSlowRequest           = 500 * time.Millisecond
	analysisSlowQuery             = 100 * time.Millisecond
	analysisSlowHTTPCall          = 500 * time.Millisecond
	analysisSlowMiddleware        = 100 * time.Millisecond
)

// FindingCode identifies a stable class of automatic request finding.
type FindingCode string

const (
	FindingPossibleNPlusOne    FindingCode = "possible_n_plus_one"
	FindingSQLDominatesRequest FindingCode = "sql_dominates_request"
	FindingSequentialHTTPCalls FindingCode = "sequential_http_calls"
	FindingCacheMissQueryBurst FindingCode = "cache_miss_query_burst"
	FindingSlowMiddleware      FindingCode = "slow_middleware"
	FindingSlowRequest         FindingCode = "slow_request"
	FindingSlowQuery           FindingCode = "slow_query"
	FindingSlowHTTPCall        FindingCode = "slow_http_call"
	FindingFailedOperation     FindingCode = "failed_operation"
	FindingHighCacheMissRate   FindingCode = "high_cache_miss_rate"
)

// FindingSeverity describes how strongly a finding should be surfaced.
type FindingSeverity string

const (
	FindingSeverityInfo    FindingSeverity = "info"
	FindingSeverityWarning FindingSeverity = "warning"
	FindingSeverityDanger  FindingSeverity = "danger"
)

// Finding is an actionable conclusion produced from a recorded request.
// EntryID points to the most useful related entry to open in the viewer.
type Finding struct {
	Code            FindingCode     `json:"code"`
	Severity        FindingSeverity `json:"severity"`
	Title           string          `json:"title"`
	Detail          string          `json:"detail,omitempty"`
	Suggestion      string          `json:"suggestion,omitempty"`
	EntryID         string          `json:"entry_id,omitempty"`
	RelatedEntryIDs []string        `json:"related_entry_ids,omitempty"`
}

// RequestAnalysis contains automatic findings for one captured HTTP request.
type RequestAnalysis struct {
	RequestID         string    `json:"request_id"`
	RequestDurationNS int64     `json:"request_duration_ns"`
	GeneratedAt       time.Time `json:"generated_at"`
	Findings          []Finding `json:"findings"`
}

type analyzedQuery struct {
	entry       Entry
	query       Query
	fingerprint string
}

type analyzedCache struct {
	entry Entry
	cache Cache
}

type analyzedHTTPCall struct {
	entry Entry
	call  HTTPCall
	host  string
}

type analyzedMiddleware struct {
	entry      Entry
	middleware Middleware
}

// AnalyzeRequest analyzes the complete stored timeline for a request. It
// returns false when requestID does not identify a retained Request entry.
func (p *Profiler) AnalyzeRequest(requestID string) (RequestAnalysis, bool) {
	if p == nil || p.store == nil || requestID == "" {
		return RequestAnalysis{}, false
	}
	request, entries, ok := p.store.requestEntries(requestID)
	if !ok || request.Kind != KindRequest {
		return RequestAnalysis{}, false
	}
	return analyzeRequest(request, entries), true
}

func analyzeRequest(request Entry, entries []Entry) RequestAnalysis {
	entries = directRequestEntries(request.ID, entries)
	queries := make([]analyzedQuery, 0)
	caches := make([]analyzedCache, 0)
	httpCalls := make([]analyzedHTTPCall, 0)
	middlewares := make([]analyzedMiddleware, 0)

	for _, entry := range entries {
		switch entry.Kind {
		case KindQuery:
			var query Query
			if json.Unmarshal(entry.Data, &query) != nil {
				continue
			}
			queries = append(queries, analyzedQuery{entry: entry, query: query, fingerprint: queryFingerprint(query.SQL)})
		case KindCache:
			var cache Cache
			if json.Unmarshal(entry.Data, &cache) != nil {
				continue
			}
			caches = append(caches, analyzedCache{entry: entry, cache: cache})
		case KindHTTPCall:
			var call HTTPCall
			if json.Unmarshal(entry.Data, &call) != nil {
				continue
			}
			httpCalls = append(httpCalls, analyzedHTTPCall{entry: entry, call: call, host: httpCallHost(call.URL)})
		case KindMiddleware:
			var middleware Middleware
			if json.Unmarshal(entry.Data, &middleware) != nil {
				continue
			}
			middlewares = append(middlewares, analyzedMiddleware{entry: entry, middleware: middleware})
		}
	}

	sort.Slice(queries, func(i, j int) bool { return entryHappenedBefore(queries[i].entry, queries[j].entry) })
	sort.Slice(caches, func(i, j int) bool { return entryHappenedBefore(caches[i].entry, caches[j].entry) })
	sort.Slice(httpCalls, func(i, j int) bool { return entryHappenedBefore(httpCalls[i].entry, httpCalls[j].entry) })
	sort.Slice(middlewares, func(i, j int) bool { return entryHappenedBefore(middlewares[i].entry, middlewares[j].entry) })

	effectiveDuration := requestAnalysisDuration(request, entries)
	cacheFindings, cacheExplainedQueries := cacheMissQueryFindings(caches, queries)
	findings := nPlusOneFindings(queries, cacheExplainedQueries)
	if finding, ok := sqlShareFinding(queries, request.StartedAt, effectiveDuration); ok {
		findings = append(findings, finding)
	}
	findings = append(findings, sequentialHTTPFindings(httpCalls)...)
	findings = append(findings, cacheFindings...)
	findings = append(findings, slowMiddlewareFindings(middlewares)...)
	findings = append(findings, legacyPerformanceFindings(request, queries, httpCalls, caches, entries)...)

	return RequestAnalysis{
		RequestID:         request.ID,
		RequestDurationNS: int64(effectiveDuration),
		GeneratedAt:       time.Now().UTC(),
		Findings:          findings,
	}
}

func nPlusOneFindings(queries []analyzedQuery, excluded map[string]struct{}) []Finding {
	groups := make(map[string][]analyzedQuery)
	for _, query := range queries {
		if query.fingerprint == "" {
			continue
		}
		if _, skip := excluded[query.entry.ID]; skip {
			continue
		}
		groups[query.fingerprint] = append(groups[query.fingerprint], query)
	}

	fingerprints := make([]string, 0, len(groups))
	for fingerprint, repeated := range groups {
		if len(repeated) >= analysisNPlusOneMinimum {
			fingerprints = append(fingerprints, fingerprint)
		}
	}
	slices.SortFunc(fingerprints, func(left, right string) int {
		if difference := len(groups[right]) - len(groups[left]); difference != 0 {
			return difference
		}
		return strings.Compare(left, right)
	})

	findings := make([]Finding, 0, len(fingerprints))
	for _, fingerprint := range fingerprints {
		repeated := groups[fingerprint]
		findings = append(findings, Finding{
			Code:            FindingPossibleNPlusOne,
			Severity:        FindingSeverityWarning,
			Title:           fmt.Sprintf("Possible N+1: query repeated %d times", len(repeated)),
			Detail:          compactFindingText(repeated[0].query.SQL, 180),
			Suggestion:      "Load the related records in one query or batch the lookup.",
			EntryID:         repeated[0].entry.ID,
			RelatedEntryIDs: queryEntryIDs(repeated),
		})
	}
	return findings
}

func sqlShareFinding(queries []analyzedQuery, requestStartedAt time.Time, requestDuration time.Duration) (Finding, bool) {
	if requestDuration <= 0 || len(queries) == 0 {
		return Finding{}, false
	}
	type interval struct {
		start time.Time
		end   time.Time
	}
	intervals := make([]interval, 0, len(queries))
	contributing := make([]analyzedQuery, 0, len(queries))
	var slowest analyzedQuery
	for _, query := range queries {
		duration := time.Duration(max(query.entry.DurationNS, 0))
		if duration <= 0 || query.entry.StartedAt.IsZero() {
			continue
		}
		start := query.entry.StartedAt
		end := start.Add(duration)
		if !requestStartedAt.IsZero() {
			requestEnd := requestStartedAt.Add(requestDuration)
			if start.Before(requestStartedAt) {
				start = requestStartedAt
			}
			if end.After(requestEnd) {
				end = requestEnd
			}
		}
		if !end.After(start) {
			continue
		}
		intervals = append(intervals, interval{start: start, end: end})
		contributing = append(contributing, query)
		if duration > time.Duration(slowest.entry.DurationNS) {
			slowest = query
		}
	}
	if len(intervals) == 0 {
		return Finding{}, false
	}
	sort.Slice(intervals, func(i, j int) bool { return intervals[i].start.Before(intervals[j].start) })
	total := intervals[0].end.Sub(intervals[0].start)
	coveredUntil := intervals[0].end
	for _, current := range intervals[1:] {
		if !current.start.After(coveredUntil) {
			if current.end.After(coveredUntil) {
				total += current.end.Sub(coveredUntil)
				coveredUntil = current.end
			}
			continue
		}
		total += current.end.Sub(current.start)
		coveredUntil = current.end
	}
	percentage := int(math.Round(float64(total) / float64(requestDuration) * 100))
	percentage = min(percentage, 100)
	if percentage < analysisSQLShareMinimum {
		return Finding{}, false
	}
	return Finding{
		Code:            FindingSQLDominatesRequest,
		Severity:        FindingSeverityWarning,
		Title:           fmt.Sprintf("SQL consumed %d%% of request", percentage),
		Detail:          fmt.Sprintf("%s wall-clock coverage across %d queries", findingDuration(total), len(contributing)),
		Suggestion:      "Inspect the slowest query and reduce query count or execution time.",
		EntryID:         slowest.entry.ID,
		RelatedEntryIDs: queryEntryIDs(contributing),
	}, true
}

func cacheMissQueryFindings(caches []analyzedCache, queries []analyzedQuery) ([]Finding, map[string]struct{}) {
	findings := make([]Finding, 0)
	explained := make(map[string]struct{})
	claimedQueries := make(map[string]struct{})
	for cacheIndex := len(caches) - 1; cacheIndex >= 0; cacheIndex-- {
		cache := caches[cacheIndex]
		if !cacheReadOperation(cache.cache.Operation) || cache.cache.Hit || cache.cache.Error != "" || strings.TrimSpace(cache.cache.Key) == "" {
			continue
		}
		first := sort.Search(len(queries), func(index int) bool {
			return !queries[index].entry.StartedAt.Before(cache.entry.StartedAt)
		})
		if first == len(queries) || queries[first].fingerprint == "" {
			continue
		}
		if queries[first].entry.StartedAt.Sub(cache.entry.StartedAt) > analysisCacheQueryMaxGap {
			continue
		}
		resolvedAt, resolved := cacheResolutionAfter(cacheIndex, caches)
		fingerprint := queries[first].fingerprint
		repeated := make([]analyzedQuery, 0)
		for _, query := range queries[first:] {
			if resolved && !query.entry.StartedAt.Before(resolvedAt) {
				break
			}
			if query.fingerprint != fingerprint {
				break
			}
			if len(repeated) > 0 {
				previous := repeated[len(repeated)-1].entry
				previousEnd := previous.StartedAt.Add(time.Duration(max(previous.DurationNS, 0)))
				if gap := query.entry.StartedAt.Sub(previousEnd); gap > analysisCacheQueryMaxGap {
					break
				}
			}
			if _, claimed := claimedQueries[query.entry.ID]; claimed {
				repeated = nil
				break
			}
			repeated = append(repeated, query)
		}
		if len(repeated) < analysisCacheBurstMinimum {
			continue
		}
		for _, query := range repeated {
			claimedQueries[query.entry.ID] = struct{}{}
			explained[query.entry.ID] = struct{}{}
		}
		entryIDs := append([]string{cache.entry.ID}, queryEntryIDs(repeated)...)
		findings = append(findings, Finding{
			Code:            FindingCacheMissQueryBurst,
			Severity:        FindingSeverityWarning,
			Title:           fmt.Sprintf("Cache miss followed by %d identical queries", len(repeated)),
			Detail:          fmt.Sprintf("Key %q · %s", cache.cache.Key, compactFindingText(repeated[0].query.SQL, 140)),
			Suggestion:      "Populate the cache once and reuse the loaded value for this request.",
			EntryID:         cache.entry.ID,
			RelatedEntryIDs: entryIDs,
		})
	}
	slices.Reverse(findings)
	return findings, explained
}

func sequentialHTTPFindings(calls []analyzedHTTPCall) []Finding {
	byHost := make(map[string][]analyzedHTTPCall)
	for _, call := range calls {
		if call.host == "" || !safeConcurrentHTTPMethod(call.call.Method) || call.entry.DurationNS <= 0 || call.call.Error != "" || call.call.Status >= 400 {
			continue
		}
		byHost[call.host] = append(byHost[call.host], call)
	}
	hosts := make([]string, 0, len(byHost))
	for host := range byHost {
		hosts = append(hosts, host)
	}
	slices.Sort(hosts)

	findings := make([]Finding, 0)
	for _, host := range hosts {
		operations := byHost[host]
		sort.Slice(operations, func(i, j int) bool { return entryHappenedBefore(operations[i].entry, operations[j].entry) })
		best := operations[:0]
		for start := 0; start < len(operations); {
			end := start + 1
			for end < len(operations) {
				previousEnd := operations[end-1].entry.StartedAt.Add(time.Duration(operations[end-1].entry.DurationNS))
				gap := operations[end].entry.StartedAt.Sub(previousEnd)
				if gap < 0 || gap > analysisSequentialHTTPMaxGap {
					break
				}
				end++
			}
			if end-start > len(best) {
				best = operations[start:end]
			}
			start = end
		}
		if len(best) < analysisSequentialHTTPMinimum {
			continue
		}
		var total time.Duration
		ids := make([]string, 0, len(best))
		for _, operation := range best {
			total += time.Duration(operation.entry.DurationNS)
			ids = append(ids, operation.entry.ID)
		}
		findings = append(findings, Finding{
			Code:            FindingSequentialHTTPCalls,
			Severity:        FindingSeverityInfo,
			Title:           fmt.Sprintf("%d sequential HTTP calls could run concurrently", len(best)),
			Detail:          fmt.Sprintf("Host %s · %s combined", host, findingDuration(total)),
			Suggestion:      "Run independent calls concurrently and preserve cancellation through context.",
			EntryID:         best[0].entry.ID,
			RelatedEntryIDs: ids,
		})
	}
	return findings
}

func slowMiddlewareFindings(middlewares []analyzedMiddleware) []Finding {
	findings := make([]Finding, 0)
	for _, operation := range middlewares {
		duration := time.Duration(operation.entry.DurationNS)
		if duration < analysisSlowMiddleware {
			continue
		}
		name := strings.TrimSpace(operation.middleware.Name)
		if name == "" {
			name = "unnamed"
		}
		findings = append(findings, Finding{
			Code:            FindingSlowMiddleware,
			Severity:        FindingSeverityWarning,
			Title:           fmt.Sprintf("Middleware %s took %s", name, findingDuration(duration)),
			Detail:          "Inclusive middleware duration exceeded 100 ms.",
			Suggestion:      "Profile work done before and after the downstream handler.",
			EntryID:         operation.entry.ID,
			RelatedEntryIDs: []string{operation.entry.ID},
		})
	}
	return findings
}

func legacyPerformanceFindings(request Entry, queries []analyzedQuery, calls []analyzedHTTPCall, caches []analyzedCache, entries []Entry) []Finding {
	findings := make([]Finding, 0)
	if duration := time.Duration(request.DurationNS); duration >= analysisSlowRequest {
		findings = append(findings, Finding{
			Code: FindingSlowRequest, Severity: FindingSeverityDanger,
			Title:      "Slow request: " + findingDuration(duration),
			Detail:     "Recorded request duration exceeded 500 ms.",
			Suggestion: "Start with the largest related operation in the timeline.",
			EntryID:    request.ID, RelatedEntryIDs: []string{request.ID},
		})
	}
	for _, query := range queries {
		duration := time.Duration(query.entry.DurationNS)
		if duration < analysisSlowQuery {
			continue
		}
		findings = append(findings, Finding{
			Code: FindingSlowQuery, Severity: FindingSeverityWarning,
			Title:      "Slow query: " + findingDuration(duration),
			Detail:     compactFindingText(query.query.SQL, 180),
			Suggestion: "Inspect the query plan, indexes, and returned row count.",
			EntryID:    query.entry.ID, RelatedEntryIDs: []string{query.entry.ID},
		})
	}
	for _, operation := range calls {
		duration := time.Duration(operation.entry.DurationNS)
		if duration < analysisSlowHTTPCall {
			continue
		}
		findings = append(findings, Finding{
			Code: FindingSlowHTTPCall, Severity: FindingSeverityWarning,
			Title:      "Slow HTTP call: " + findingDuration(duration),
			Detail:     compactFindingText(strings.TrimSpace(operation.call.Method+" "+operation.call.URL), 180),
			Suggestion: "Check the upstream latency, timeout, retries, and response size.",
			EntryID:    operation.entry.ID, RelatedEntryIDs: []string{operation.entry.ID},
		})
	}
	findings = append(findings, failedOperationFindings(entries)...)
	if finding, ok := cacheMissRateFinding(caches); ok {
		findings = append(findings, finding)
	}
	return findings
}

func failedOperationFindings(entries []Entry) []Finding {
	findings := make([]Finding, 0)
	for _, entry := range entries {
		var label, detail string
		switch entry.Kind {
		case KindJob:
			var job Job
			if json.Unmarshal(entry.Data, &job) != nil || job.Error == "" && !strings.EqualFold(job.State, "failed") {
				continue
			}
			label, detail = "Job", job.Name
		case KindEmail:
			var email Email
			if json.Unmarshal(entry.Data, &email) != nil || email.Error == "" && !strings.EqualFold(email.Status, "failed") && !strings.EqualFold(email.Status, "bounced") {
				continue
			}
			label, detail = "Mail", email.Subject
		case KindHTTPCall:
			var call HTTPCall
			if json.Unmarshal(entry.Data, &call) != nil || call.Error == "" && call.Status < 400 {
				continue
			}
			label, detail = "HTTP call", strings.TrimSpace(call.Method+" "+call.URL)
		default:
			continue
		}
		findings = append(findings, Finding{
			Code: FindingFailedOperation, Severity: FindingSeverityDanger,
			Title:      "Failed " + label,
			Detail:     compactFindingText(detail, 180),
			Suggestion: "Inspect the recorded error and retry state.",
			EntryID:    entry.ID, RelatedEntryIDs: []string{entry.ID},
		})
	}
	return findings
}

func cacheMissRateFinding(caches []analyzedCache) (Finding, bool) {
	reads := make([]analyzedCache, 0, len(caches))
	for _, operation := range caches {
		if cacheReadOperation(operation.cache.Operation) && operation.cache.Error == "" {
			reads = append(reads, operation)
		}
	}
	if len(reads) < analysisCacheMissRateMinimum {
		return Finding{}, false
	}
	misses := make([]string, 0, len(reads))
	for _, operation := range reads {
		if !operation.cache.Hit {
			misses = append(misses, operation.entry.ID)
		}
	}
	percentage := int(math.Round(float64(len(misses)) / float64(len(reads)) * 100))
	if percentage < 50 {
		return Finding{}, false
	}
	return Finding{
		Code: FindingHighCacheMissRate, Severity: FindingSeverityWarning,
		Title:      fmt.Sprintf("Cache miss rate is %d%%", percentage),
		Detail:     fmt.Sprintf("%d of %d cache reads missed", len(misses), len(reads)),
		Suggestion: "Review cache keys, TTLs, warming, and invalidation behavior.",
		EntryID:    misses[0], RelatedEntryIDs: misses,
	}, true
}

func directRequestEntries(requestID string, entries []Entry) []Entry {
	direct := make([]Entry, 0, len(entries))
	for _, entry := range entries {
		if entry.ID == requestID || entry.RequestID == requestID {
			direct = append(direct, entry)
		}
	}
	return direct
}

func cacheResolutionAfter(index int, caches []analyzedCache) (time.Time, bool) {
	miss := caches[index]
	for _, candidate := range caches[index+1:] {
		if !sameCacheResource(miss.cache, candidate.cache) || candidate.cache.Error != "" {
			continue
		}
		if cacheWriteOperation(candidate.cache.Operation) || cacheReadOperation(candidate.cache.Operation) && candidate.cache.Hit {
			return candidate.entry.StartedAt, true
		}
	}
	return time.Time{}, false
}

func sameCacheResource(left, right Cache) bool {
	if strings.TrimSpace(left.Key) == "" || left.Key != right.Key {
		return false
	}
	return left.Store == "" || right.Store == "" || strings.EqualFold(left.Store, right.Store)
}

func cacheReadOperation(operation string) bool {
	operation = strings.ToLower(strings.TrimSpace(operation))
	if operation == "" || strings.HasPrefix(operation, "get_") {
		return true
	}
	switch operation {
	case "get", "mget", "hget", "hmget", "hgetall", "has", "exists", "hexists", "pull", "remember", "remember_forever", "lock_get":
		return true
	default:
		return false
	}
}

func cacheWriteOperation(operation string) bool {
	operation = strings.ToLower(strings.TrimSpace(operation))
	switch operation {
	case "put", "set", "setex", "psetex", "setnx", "mset", "hset", "hmset", "add", "forever", "increment", "decrement", "incr", "decr", "incrby", "decrby", "getset":
		return true
	default:
		return false
	}
}

func safeConcurrentHTTPMethod(method string) bool {
	switch strings.ToUpper(strings.TrimSpace(method)) {
	case "GET", "HEAD", "OPTIONS":
		return true
	default:
		return false
	}
}

func requestAnalysisDuration(request Entry, entries []Entry) time.Duration {
	duration := time.Duration(max(request.DurationNS, 0))
	if request.StartedAt.IsZero() {
		return duration
	}
	for _, entry := range entries {
		if entry.ID == request.ID || entry.StartedAt.IsZero() || entry.StartedAt.Before(request.StartedAt) {
			continue
		}
		end := entry.StartedAt.Add(time.Duration(max(entry.DurationNS, 0)))
		if span := end.Sub(request.StartedAt); span > duration {
			duration = span
		}
	}
	return duration
}

func queryEntryIDs(queries []analyzedQuery) []string {
	ids := make([]string, 0, len(queries))
	for _, query := range queries {
		ids = append(ids, query.entry.ID)
	}
	return ids
}

func entryHappenedBefore(left, right Entry) bool {
	if left.StartedAt.Equal(right.StartedAt) {
		return left.Cursor < right.Cursor
	}
	return left.StartedAt.Before(right.StartedAt)
}

func httpCallHost(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return strings.ToLower(parsed.Hostname())
}

func queryFingerprint(sql string) string {
	var result strings.Builder
	result.Grow(len(sql))
	spacePending := false
	for index := 0; index < len(sql); {
		character := rune(sql[index])
		if unicode.IsSpace(character) {
			spacePending = result.Len() > 0
			index++
			continue
		}
		if spacePending {
			result.WriteByte(' ')
			spacePending = false
		}
		if sql[index] == '\'' {
			result.WriteByte('?')
			index++
			for index < len(sql) {
				if sql[index] == '\'' {
					index++
					if index < len(sql) && sql[index] == '\'' {
						index++
						continue
					}
					break
				}
				index++
			}
			continue
		}
		if isSQLNumberStart(sql, index) {
			result.WriteByte('?')
			index++
			for index < len(sql) && (sql[index] >= '0' && sql[index] <= '9' || sql[index] == '.') {
				index++
			}
			continue
		}
		result.WriteRune(unicode.ToLower(character))
		index++
	}
	return strings.TrimSpace(strings.TrimSuffix(result.String(), ";"))
}

func isSQLNumberStart(sql string, index int) bool {
	if sql[index] < '0' || sql[index] > '9' {
		return false
	}
	if index == 0 {
		return true
	}
	previous := rune(sql[index-1])
	return !unicode.IsLetter(previous) && !unicode.IsDigit(previous) && previous != '_'
}

func compactFindingText(value string, limit int) string {
	value = strings.Join(strings.Fields(value), " ")
	if len(value) <= limit {
		return value
	}
	return strings.TrimSpace(value[:limit-1]) + "…"
}

func findingDuration(value time.Duration) string {
	if value >= time.Second {
		return fmt.Sprintf("%.2f s", float64(value)/float64(time.Second))
	}
	if value >= time.Millisecond {
		milliseconds := float64(value) / float64(time.Millisecond)
		if math.Mod(milliseconds, 1) == 0 {
			return fmt.Sprintf("%.0f ms", milliseconds)
		}
		return fmt.Sprintf("%.1f ms", milliseconds)
	}
	if value >= time.Microsecond {
		return fmt.Sprintf("%.0f µs", float64(value)/float64(time.Microsecond))
	}
	return value.String()
}
