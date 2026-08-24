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
	analysisSlowSchedule          = 500 * time.Millisecond
	analysisSlowCallable          = 500 * time.Millisecond
	analysisSlowTask              = time.Second
	analysisBottleneckQuery       = 50 * time.Millisecond
	analysisSlowQuery             = 100 * time.Millisecond
	analysisVerySlowQuery         = 500 * time.Millisecond
	analysisSlowCache             = 50 * time.Millisecond
	analysisSlowHTTPCall          = 500 * time.Millisecond
	analysisSlowMiddleware        = 100 * time.Millisecond
	analysisSlowEvent             = 500 * time.Millisecond
	analysisSlowJob               = 500 * time.Millisecond
	analysisSlowEmail             = 500 * time.Millisecond
	analysisBottleneckShare       = 50
)

// FindingCode identifies a stable class of automatic request finding.
type FindingCode string

const (
	// FindingPossibleNPlusOne reports repeated structurally equivalent queries.
	FindingPossibleNPlusOne FindingCode = "possible_n_plus_one"
	// FindingSQLDominatesRequest reports requests that spend most of their time in SQL.
	FindingSQLDominatesRequest FindingCode = "sql_dominates_request"
	// FindingSQLDominatesSchedule reports schedules that spend most of their time in SQL.
	FindingSQLDominatesSchedule FindingCode = "sql_dominates_schedule"
	// FindingSQLDominatesCallable reports callables that spend most of their time in SQL.
	FindingSQLDominatesCallable FindingCode = "sql_dominates_callable"
	// FindingSQLDominatesTask reports tasks that spend most of their time in SQL.
	FindingSQLDominatesTask FindingCode = "sql_dominates_task"
	// FindingSequentialHTTPCalls reports outbound calls that appear to run serially.
	FindingSequentialHTTPCalls FindingCode = "sequential_http_calls"
	// FindingCacheMissQueryBurst reports repeated cache misses followed by queries.
	FindingCacheMissQueryBurst FindingCode = "cache_miss_query_burst"
	// FindingSlowMiddleware reports middleware above the built-in duration threshold.
	FindingSlowMiddleware FindingCode = "slow_middleware"
	// FindingSlowRequest reports requests above the built-in duration threshold.
	FindingSlowRequest FindingCode = "slow_request"
	// FindingSlowSchedule reports schedule executions above the built-in duration threshold.
	FindingSlowSchedule FindingCode = "slow_schedule"
	// FindingSlowCallable reports callable executions above the built-in duration threshold.
	FindingSlowCallable FindingCode = "slow_callable"
	// FindingSlowTask reports tasks above the built-in duration threshold.
	FindingSlowTask FindingCode = "slow_task"
	// FindingSlowQuery reports queries above the built-in duration threshold.
	FindingSlowQuery FindingCode = "slow_query"
	// FindingSlowHTTPCall reports outbound calls above the built-in duration threshold.
	FindingSlowHTTPCall FindingCode = "slow_http_call"
	// FindingSlowEvent reports measured custom events above the built-in duration threshold.
	FindingSlowEvent FindingCode = "slow_event"
	// FindingExecutionBottleneck reports the child operation dominating an execution.
	FindingExecutionBottleneck FindingCode = "execution_bottleneck"
	// FindingQueryPlanIssue reports a normalized concern found in a stored EXPLAIN plan.
	FindingQueryPlanIssue FindingCode = "query_plan_issue"
	// FindingFailedOperation reports a related operation carrying an error or failed status.
	FindingFailedOperation FindingCode = "failed_operation"
	// FindingHighCacheMissRate reports request timelines dominated by cache misses.
	FindingHighCacheMissRate FindingCode = "high_cache_miss_rate"
)

// FindingSeverity describes how strongly a finding should be surfaced.
type FindingSeverity string

const (
	// FindingSeverityInfo marks an informational optimization opportunity.
	FindingSeverityInfo FindingSeverity = "info"
	// FindingSeverityWarning marks a likely performance or reliability issue.
	FindingSeverityWarning FindingSeverity = "warning"
	// FindingSeverityDanger marks a failed or especially costly operation.
	FindingSeverityDanger FindingSeverity = "danger"
)

// Finding is an actionable conclusion produced from a recorded execution.
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

// ScheduleAnalysis contains automatic findings for one captured Schedule execution.
type ScheduleAnalysis struct {
	ScheduleID         string    `json:"schedule_id"`
	ScheduleDurationNS int64     `json:"schedule_duration_ns"`
	GeneratedAt        time.Time `json:"generated_at"`
	Findings           []Finding `json:"findings"`
}

// CallableAnalysis contains automatic findings for one captured Callable execution.
type CallableAnalysis struct {
	CallableID         string    `json:"callable_id"`
	CallableDurationNS int64     `json:"callable_duration_ns"`
	GeneratedAt        time.Time `json:"generated_at"`
	Findings           []Finding `json:"findings"`
}

// TaskAnalysis contains automatic findings for one captured Task execution.
type TaskAnalysis struct {
	TaskID         string    `json:"task_id"`
	TaskDurationNS int64     `json:"task_duration_ns"`
	GeneratedAt    time.Time `json:"generated_at"`
	Findings       []Finding `json:"findings"`
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

type analyzedEvent struct {
	entry Entry
	event Event
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

// AnalyzeSchedule analyzes the complete ParentID hierarchy for a Schedule. It
// returns false when scheduleID does not identify a retained Schedule entry.
func (p *Profiler) AnalyzeSchedule(scheduleID string) (ScheduleAnalysis, bool) {
	if p == nil || p.store == nil || scheduleID == "" {
		return ScheduleAnalysis{}, false
	}
	schedule, entries, ok := p.store.scopeEntries(scheduleID)
	if !ok || schedule.Kind != KindSchedule {
		return ScheduleAnalysis{}, false
	}
	return analyzeSchedule(schedule, entries), true
}

// AnalyzeCallable analyzes the complete ParentID hierarchy for a Callable. It
// returns false when callableID does not identify a retained Callable entry.
func (p *Profiler) AnalyzeCallable(callableID string) (CallableAnalysis, bool) {
	if p == nil || p.store == nil || callableID == "" {
		return CallableAnalysis{}, false
	}
	callable, entries, ok := p.store.scopeEntries(callableID)
	if !ok || callable.Kind != KindCallable {
		return CallableAnalysis{}, false
	}
	return analyzeCallable(callable, entries), true
}

// AnalyzeTask analyzes the complete ParentID hierarchy for a Task. It returns
// false when taskID does not identify a retained Task entry.
func (p *Profiler) AnalyzeTask(taskID string) (TaskAnalysis, bool) {
	if p == nil || p.store == nil || taskID == "" {
		return TaskAnalysis{}, false
	}
	task, entries, ok := p.store.scopeEntries(taskID)
	if !ok || task.Kind != KindTask {
		return TaskAnalysis{}, false
	}
	return analyzeTask(task, entries), true
}

func analyzeRequest(request Entry, entries []Entry) RequestAnalysis {
	entries = directRequestEntries(request.ID, entries)
	duration, findings := analyzeExecution(request, entries, "request", FindingSQLDominatesRequest)
	return RequestAnalysis{
		RequestID:         request.ID,
		RequestDurationNS: int64(duration),
		GeneratedAt:       time.Now().UTC(),
		Findings:          findings,
	}
}

func analyzeSchedule(schedule Entry, entries []Entry) ScheduleAnalysis {
	duration, findings := analyzeExecution(schedule, entries, "schedule", FindingSQLDominatesSchedule)
	return ScheduleAnalysis{
		ScheduleID:         schedule.ID,
		ScheduleDurationNS: int64(duration),
		GeneratedAt:        time.Now().UTC(),
		Findings:           findings,
	}
}

func analyzeCallable(callable Entry, entries []Entry) CallableAnalysis {
	duration, findings := analyzeExecution(callable, entries, "callable", FindingSQLDominatesCallable)
	return CallableAnalysis{
		CallableID:         callable.ID,
		CallableDurationNS: int64(duration),
		GeneratedAt:        time.Now().UTC(),
		Findings:           findings,
	}
}

func analyzeTask(task Entry, entries []Entry) TaskAnalysis {
	duration, findings := analyzeExecution(task, entries, "task", FindingSQLDominatesTask)
	return TaskAnalysis{TaskID: task.ID, TaskDurationNS: int64(duration), GeneratedAt: time.Now().UTC(), Findings: findings}
}

func analyzeExecution(root Entry, entries []Entry, label string, sqlDominatesCode FindingCode) (time.Duration, []Finding) {
	queries := make([]analyzedQuery, 0)
	caches := make([]analyzedCache, 0)
	httpCalls := make([]analyzedHTTPCall, 0)
	middlewares := make([]analyzedMiddleware, 0)
	events := make([]analyzedEvent, 0)

	for _, entry := range entries {
		switch entry.Kind {
		case KindQuery:
			var query Query
			if json.Unmarshal(entry.Data, &query) != nil {
				continue
			}
			fingerprint := queryFingerprint(query.SQL)
			queries = append(queries, analyzedQuery{entry: entry, query: query, fingerprint: fingerprint})
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
		case KindEvent:
			var event Event
			if json.Unmarshal(entry.Data, &event) != nil {
				continue
			}
			events = append(events, analyzedEvent{entry: entry, event: event})
		}
	}

	sort.Slice(queries, func(i, j int) bool { return entryHappenedBefore(queries[i].entry, queries[j].entry) })
	sort.Slice(caches, func(i, j int) bool { return entryHappenedBefore(caches[i].entry, caches[j].entry) })
	sort.Slice(httpCalls, func(i, j int) bool { return entryHappenedBefore(httpCalls[i].entry, httpCalls[j].entry) })
	sort.Slice(middlewares, func(i, j int) bool { return entryHappenedBefore(middlewares[i].entry, middlewares[j].entry) })
	sort.Slice(events, func(i, j int) bool { return entryHappenedBefore(events[i].entry, events[j].entry) })

	effectiveDuration := executionAnalysisDuration(root, entries)
	hierarchy := newAnalysisEntryHierarchy(entries)
	cacheFindings, cacheExplainedQueries := cacheMissQueryFindings(caches, queries)
	findings := nPlusOneFindings(queries, cacheExplainedQueries)
	if finding, ok := sqlShareFinding(queries, root.StartedAt, effectiveDuration, label, sqlDominatesCode); ok {
		findings = append(findings, finding)
	}
	findings = append(findings, sequentialHTTPFindings(httpCalls)...)
	findings = append(findings, cacheFindings...)
	findings = append(findings, slowMiddlewareFindings(middlewares, hierarchy)...)
	findings = append(findings, slowEventFindings(events)...)
	if finding, ok := executionBottleneckFinding(root, entries, hierarchy, effectiveDuration); ok {
		findings = append(findings, finding)
	}
	findings = append(findings, queryPlanFindings(queries)...)
	findings = append(findings, legacyPerformanceFindings(root, queries, httpCalls, caches, entries, len(cacheFindings) == 0)...)

	return effectiveDuration, findings
}

func nPlusOneFindings(queries []analyzedQuery, excluded map[string]struct{}) []Finding {
	groups := make(map[string][]analyzedQuery)
	for _, query := range queries {
		group := queryNPlusOneGroup(query)
		if group == "" {
			continue
		}
		if _, skip := excluded[query.entry.ID]; skip {
			continue
		}
		groups[group] = append(groups[group], query)
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

func sqlShareFinding(queries []analyzedQuery, executionStartedAt time.Time, executionDuration time.Duration, executionLabel string, code FindingCode) (Finding, bool) {
	if executionDuration <= 0 || len(queries) == 0 {
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
		if !executionStartedAt.IsZero() {
			executionEnd := executionStartedAt.Add(executionDuration)
			if start.Before(executionStartedAt) {
				start = executionStartedAt
			}
			if end.After(executionEnd) {
				end = executionEnd
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
	percentage := int(math.Round(float64(total) / float64(executionDuration) * 100))
	percentage = min(percentage, 100)
	if percentage < analysisSQLShareMinimum {
		return Finding{}, false
	}
	return Finding{
		Code:            code,
		Severity:        FindingSeverityWarning,
		Title:           fmt.Sprintf("SQL consumed %d%% of %s", percentage, executionLabel),
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
			Suggestion:      "Populate the cache once and reuse the loaded value for this execution.",
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

func slowMiddlewareFindings(middlewares []analyzedMiddleware, hierarchy analysisEntryHierarchy) []Finding {
	findings := make([]Finding, 0)
	for _, operation := range middlewares {
		duration := middlewareWorkDuration(operation.middleware, operation.entry.DurationNS)
		if duration < analysisSlowMiddleware {
			continue
		}
		if operation.middleware.WorkDuration == nil && hierarchy.hasExplainingDescendant(operation.entry.ID, duration) {
			continue
		}
		name := strings.TrimSpace(operation.middleware.Name)
		if name == "" {
			name = "unnamed"
		}
		detail := "Measured middleware work duration exceeded 100 ms; time delegated to the downstream handler is excluded."
		if operation.middleware.WorkDuration == nil {
			detail = "The middleware's complete invocation span exceeded 100 ms; exact work timing was not available and no nested operation explained most of the span."
		}
		findings = append(findings, Finding{
			Code:            FindingSlowMiddleware,
			Severity:        FindingSeverityWarning,
			Title:           fmt.Sprintf("Middleware %s took %s", name, findingDuration(duration)),
			Detail:          detail,
			Suggestion:      "Inspect the middleware's nested operations and work before or after the downstream handler.",
			EntryID:         operation.entry.ID,
			RelatedEntryIDs: []string{operation.entry.ID},
		})
	}
	return findings
}

func middlewareWorkDuration(middleware Middleware, fallback int64) time.Duration {
	if middleware.WorkDuration != nil {
		return max(*middleware.WorkDuration, 0)
	}
	return time.Duration(max(fallback, 0))
}

func slowEventFindings(events []analyzedEvent) []Finding {
	findings := make([]Finding, 0)
	for _, operation := range events {
		duration := time.Duration(operation.entry.DurationNS)
		if duration < analysisSlowEvent {
			continue
		}
		name := strings.TrimSpace(operation.event.Name)
		if name == "" {
			name = "unnamed"
		}
		findings = append(findings, Finding{
			Code:            FindingSlowEvent,
			Severity:        FindingSeverityWarning,
			Title:           fmt.Sprintf("Event %s took %s", name, findingDuration(duration)),
			Detail:          "Measured event duration exceeded 500 ms.",
			Suggestion:      "Inspect this operation and split or instrument its expensive stages.",
			EntryID:         operation.entry.ID,
			RelatedEntryIDs: []string{operation.entry.ID},
		})
	}
	return findings
}

func executionBottleneckFinding(root Entry, entries []Entry, hierarchy analysisEntryHierarchy, executionDuration time.Duration) (Finding, bool) {
	if executionDuration <= 0 {
		return Finding{}, false
	}
	var bottleneck Entry
	bottleneckDuration := time.Duration(0)
	for _, entry := range entries {
		duration := entryBottleneckDuration(entry)
		if entry.ID == root.ID || duration <= bottleneckDuration || duration < bottleneckMinimum(entry.Kind) ||
			hierarchy.hasQualifyingExplainingDescendant(entry.ID, duration) {
			continue
		}
		bottleneck = entry
		bottleneckDuration = duration
	}
	duration := bottleneckDuration
	share := int(math.Round(float64(duration) / float64(executionDuration) * 100))
	share = min(share, 100)
	if share < analysisBottleneckShare {
		return Finding{}, false
	}
	label, name := findingEntryLabel(bottleneck)
	return Finding{
		Code:            FindingExecutionBottleneck,
		Severity:        FindingSeverityWarning,
		Title:           fmt.Sprintf("Bottleneck: %s %s took %s", label, name, findingDuration(duration)),
		Detail:          fmt.Sprintf("This operation accounts for about %d%% of the execution duration.", share),
		Suggestion:      "Open the operation and instrument its children to isolate the expensive work.",
		EntryID:         bottleneck.ID,
		RelatedEntryIDs: []string{bottleneck.ID},
	}, true
}

type analysisEntryHierarchy struct {
	maxDescendantDuration           map[string]time.Duration
	maxQualifyingDescendantDuration map[string]time.Duration
}

func newAnalysisEntryHierarchy(entries []Entry) analysisEntryHierarchy {
	entriesByID := make(map[string]Entry, len(entries))
	for _, entry := range entries {
		if entry.ID != "" {
			entriesByID[entry.ID] = entry
		}
	}

	children := make(map[string][]string, len(entriesByID))
	for id, entry := range entriesByID {
		if entry.ParentID == "" {
			continue
		}
		if _, ok := entriesByID[entry.ParentID]; ok {
			children[entry.ParentID] = append(children[entry.ParentID], id)
		}
	}

	hierarchy := analysisEntryHierarchy{
		maxDescendantDuration:           make(map[string]time.Duration, len(entriesByID)),
		maxQualifyingDescendantDuration: make(map[string]time.Duration, len(entriesByID)),
	}
	state := make(map[string]uint8, len(entriesByID))
	type subtreeDurations struct {
		all        time.Duration
		qualifying time.Duration
	}
	memo := make(map[string]subtreeDurations, len(entriesByID))
	var subtreeMaximum func(string) subtreeDurations
	subtreeMaximum = func(id string) subtreeDurations {
		switch state[id] {
		case 1:
			return subtreeDurations{}
		case 2:
			return memo[id]
		}
		state[id] = 1
		entry := entriesByID[id]
		ownDuration := time.Duration(0)
		if bottleneckCandidate(entry.Kind) {
			ownDuration = entryBottleneckDuration(entry)
		}
		maxDescendant := subtreeDurations{}
		for _, childID := range children[id] {
			childMaximum := subtreeMaximum(childID)
			maxDescendant.all = max(maxDescendant.all, childMaximum.all)
			maxDescendant.qualifying = max(maxDescendant.qualifying, childMaximum.qualifying)
		}
		hierarchy.maxDescendantDuration[id] = maxDescendant.all
		hierarchy.maxQualifyingDescendantDuration[id] = maxDescendant.qualifying
		result := subtreeDurations{all: max(ownDuration, maxDescendant.all), qualifying: maxDescendant.qualifying}
		if ownDuration >= bottleneckMinimum(entry.Kind) {
			result.qualifying = max(ownDuration, maxDescendant.qualifying)
		}
		memo[id] = result
		state[id] = 2
		return memo[id]
	}
	for id := range entriesByID {
		subtreeMaximum(id)
	}
	return hierarchy
}

// hasExplainingDescendant reports whether a deeper operation accounts for
// enough of an inclusive span to be the more useful bottleneck. Parent spans
// remain candidates when their children only explain a minority of the work.
func (hierarchy analysisEntryHierarchy) hasExplainingDescendant(entryID string, duration time.Duration) bool {
	if entryID == "" || duration <= 0 {
		return false
	}
	descendantDuration := hierarchy.maxDescendantDuration[entryID]
	return descendantDuration > 0 && float64(descendantDuration)/float64(duration)*100 >= analysisBottleneckShare
}

func (hierarchy analysisEntryHierarchy) hasQualifyingExplainingDescendant(entryID string, duration time.Duration) bool {
	if entryID == "" || duration <= 0 {
		return false
	}
	descendantDuration := hierarchy.maxQualifyingDescendantDuration[entryID]
	return descendantDuration > 0 && float64(descendantDuration)/float64(duration)*100 >= analysisBottleneckShare
}

func entryBottleneckDuration(entry Entry) time.Duration {
	if entry.Kind != KindMiddleware {
		return time.Duration(max(entry.DurationNS, 0))
	}
	var middleware Middleware
	if json.Unmarshal(entry.Data, &middleware) != nil {
		return time.Duration(max(entry.DurationNS, 0))
	}
	return middlewareWorkDuration(middleware, entry.DurationNS)
}

func bottleneckCandidate(kind Kind) bool {
	switch kind {
	case KindQuery, KindCache, KindHTTPCall, KindMiddleware, KindEvent, KindJob, KindEmail:
		return true
	default:
		return false
	}
}

func bottleneckMinimum(kind Kind) time.Duration {
	switch kind {
	case KindQuery:
		return analysisBottleneckQuery
	case KindCache:
		return analysisSlowCache
	case KindMiddleware:
		return analysisSlowMiddleware
	case KindHTTPCall:
		return analysisSlowHTTPCall
	case KindEvent:
		return analysisSlowEvent
	case KindJob:
		return analysisSlowJob
	case KindEmail:
		return analysisSlowEmail
	default:
		return time.Duration(1<<63 - 1)
	}
}

func findingEntryLabel(entry Entry) (string, string) {
	switch entry.Kind {
	case KindEvent:
		var event Event
		if json.Unmarshal(entry.Data, &event) == nil {
			return "event", compactFindingText(event.Name, 80)
		}
	case KindQuery:
		var query Query
		if json.Unmarshal(entry.Data, &query) == nil {
			return "query", compactFindingText(query.SQL, 80)
		}
	case KindHTTPCall:
		var call HTTPCall
		if json.Unmarshal(entry.Data, &call) == nil {
			return "HTTP call", compactFindingText(strings.TrimSpace(call.Method+" "+call.URL), 80)
		}
	case KindMiddleware:
		var middleware Middleware
		if json.Unmarshal(entry.Data, &middleware) == nil {
			return "middleware", compactFindingText(middleware.Name, 80)
		}
	}
	return string(entry.Kind), "operation"
}

func queryPlanFindings(queries []analyzedQuery) []Finding {
	findings := make([]Finding, 0)
	for _, analyzed := range queries {
		plan := analyzed.query.Plan
		if plan == nil || strings.TrimSpace(plan.Text) == "" {
			continue
		}
		issues := plan.Issues
		if len(issues) == 0 {
			issues = DetectQueryPlanIssues(analyzed.query.Driver, plan.Text)
		}
		visible := make([]QueryPlanIssue, 0, len(issues))
		for _, issue := range issues {
			if issue.Code == QueryPlanIssueLargeEstimate || time.Duration(analyzed.entry.DurationNS) >= analysisSlowQuery {
				visible = append(visible, issue)
			}
		}
		if len(visible) == 0 {
			continue
		}
		labels := make([]string, 0, len(visible))
		for _, issue := range visible {
			labels = append(labels, queryPlanIssueLabel(issue))
		}
		findings = append(findings, Finding{
			Code:            FindingQueryPlanIssue,
			Severity:        FindingSeverityWarning,
			Title:           "EXPLAIN: " + strings.Join(labels, ", "),
			Detail:          compactFindingText(visible[0].Detail, 180),
			Suggestion:      "Review filters, indexes, join order, and estimated row counts in the stored plan.",
			EntryID:         analyzed.entry.ID,
			RelatedEntryIDs: []string{analyzed.entry.ID},
		})
	}
	return findings
}

func queryPlanIssueLabel(issue QueryPlanIssue) string {
	switch issue.Code {
	case QueryPlanIssueFullScan:
		if issue.Relation != "" {
			return "full scan on " + issue.Relation
		}
		return "full table scan"
	case QueryPlanIssueTemporarySort:
		return "temporary sort"
	case QueryPlanIssueLargeEstimate:
		return fmt.Sprintf("large estimate (%d rows)", issue.EstimatedRows)
	default:
		return string(issue.Code)
	}
}

func legacyPerformanceFindings(root Entry, queries []analyzedQuery, calls []analyzedHTTPCall, caches []analyzedCache, entries []Entry, includeCacheMissRate bool) []Finding {
	findings := make([]Finding, 0)
	duration := time.Duration(root.DurationNS)
	switch root.Kind {
	case KindRequest:
		if duration >= analysisSlowRequest {
			findings = append(findings, Finding{
				Code: FindingSlowRequest, Severity: FindingSeverityDanger,
				Title:      "Slow request: " + findingDuration(duration),
				Detail:     "Recorded request duration exceeded 500 ms.",
				Suggestion: "Start with the largest related operation in the timeline.",
				EntryID:    root.ID, RelatedEntryIDs: []string{root.ID},
			})
		}
	case KindSchedule:
		if duration >= analysisSlowSchedule {
			findings = append(findings, Finding{
				Code: FindingSlowSchedule, Severity: FindingSeverityWarning,
				Title:      "Slow schedule: " + findingDuration(duration),
				Detail:     "Recorded schedule duration exceeded 500 ms.",
				Suggestion: "Start with the largest child operation in the execution timeline.",
				EntryID:    root.ID, RelatedEntryIDs: []string{root.ID},
			})
		}
	case KindCallable:
		if duration >= analysisSlowCallable {
			findings = append(findings, Finding{
				Code: FindingSlowCallable, Severity: FindingSeverityWarning,
				Title:      "Slow callable: " + findingDuration(duration),
				Detail:     "Recorded callable duration exceeded 500 ms.",
				Suggestion: "Start with the largest child operation in the execution timeline.",
				EntryID:    root.ID, RelatedEntryIDs: []string{root.ID},
			})
		}
	case KindTask:
		if duration >= analysisSlowTask {
			findings = append(findings, Finding{
				Code: FindingSlowTask, Severity: FindingSeverityWarning,
				Title:      "Slow task: " + findingDuration(duration),
				Detail:     "Recorded task duration exceeded 1 s.",
				Suggestion: "Start with the largest child operation in the execution timeline.",
				EntryID:    root.ID, RelatedEntryIDs: []string{root.ID},
			})
		}
	}
	for _, query := range queries {
		duration := time.Duration(query.entry.DurationNS)
		if duration < analysisSlowQuery {
			continue
		}
		severity := FindingSeverityWarning
		title := "Slow query: " + findingDuration(duration)
		if duration >= analysisVerySlowQuery {
			severity = FindingSeverityDanger
			title = "Very slow query: " + findingDuration(duration)
		}
		findings = append(findings, Finding{
			Code: FindingSlowQuery, Severity: severity,
			Title:      title,
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
	findings = append(findings, failedOperationFindings(entries, root.Kind == KindSchedule || root.Kind == KindCallable || root.Kind == KindTask)...)
	if includeCacheMissRate {
		if finding, ok := cacheMissRateFinding(caches); ok {
			findings = append(findings, finding)
		}
	}
	return findings
}

func failedOperationFindings(entries []Entry, includeExecutionRoots bool) []Finding {
	findings := make([]Finding, 0)
	for _, entry := range entries {
		var label, detail string
		switch entry.Kind {
		case KindRequest:
			var request Request
			if json.Unmarshal(entry.Data, &request) != nil || request.Error == "" && request.Status < 500 {
				continue
			}
			label, detail = "request", strings.TrimSpace(request.Method+" "+request.Path)
		case KindQuery:
			var query Query
			if json.Unmarshal(entry.Data, &query) != nil || query.Error == "" {
				continue
			}
			label, detail = "query", query.SQL
		case KindCache:
			var cache Cache
			if json.Unmarshal(entry.Data, &cache) != nil || cache.Error == "" {
				continue
			}
			label, detail = "cache operation", strings.TrimSpace(cache.Operation+" "+cache.Key)
		case KindEvent:
			var event Event
			if json.Unmarshal(entry.Data, &event) != nil || event.Error == "" && !failedState(event.Status) {
				continue
			}
			label, detail = "event", event.Name
		case KindMiddleware:
			var middleware Middleware
			if json.Unmarshal(entry.Data, &middleware) != nil || middleware.Error == "" && !failedState(middleware.State) {
				continue
			}
			label, detail = "middleware", middleware.Name
		case KindException:
			var exception Exception
			if json.Unmarshal(entry.Data, &exception) != nil {
				continue
			}
			label, detail = "exception", strings.TrimSpace(exception.Type+" "+exception.Message)
		case KindJob:
			var job Job
			if json.Unmarshal(entry.Data, &job) != nil || job.Error == "" && !failedState(job.State) {
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
		case KindSchedule:
			if !includeExecutionRoots {
				continue
			}
			var schedule Schedule
			if json.Unmarshal(entry.Data, &schedule) != nil || schedule.Error == "" && schedule.Panic == "" && !failedState(schedule.State) {
				continue
			}
			label, detail = "scheduled task", schedule.Name
		case KindCallable:
			if !includeExecutionRoots {
				continue
			}
			var callable Callable
			if json.Unmarshal(entry.Data, &callable) != nil || callable.Error == "" && callable.Panic == "" && !failedState(callable.State) {
				continue
			}
			label, detail = "callable", callable.Name
		case KindTask:
			if !includeExecutionRoots {
				continue
			}
			var task Task
			if json.Unmarshal(entry.Data, &task) != nil || task.Error == "" && task.Panic == "" && !failedState(task.State) {
				continue
			}
			label, detail = "task", task.Name
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

func executionAnalysisDuration(root Entry, entries []Entry) time.Duration {
	duration := time.Duration(max(root.DurationNS, 0))
	if root.StartedAt.IsZero() {
		return duration
	}
	for _, entry := range entries {
		if entry.ID == root.ID || entry.StartedAt.IsZero() || entry.StartedAt.Before(root.StartedAt) {
			continue
		}
		end := entry.StartedAt.Add(time.Duration(max(entry.DurationNS, 0)))
		if span := end.Sub(root.StartedAt); span > duration {
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
	return strings.ToLower(parsed.Host)
}

func queryNPlusOneGroup(query analyzedQuery) string {
	operation := strings.ToUpper(strings.TrimSpace(query.query.Operation))
	if operation == "" {
		operation = sqlStatementOperation(query.query.SQL)
	}
	if operation != "SELECT" && operation != "WITH" || query.fingerprint == "" {
		return ""
	}
	locality := query.entry.ParentID
	if len(query.query.Callsite) > 0 {
		frame := query.query.Callsite[0]
		locality = frame.File + ":" + fmt.Sprint(frame.Line) + ":" + frame.Function
	}
	return strings.ToLower(query.query.Connection) + "\x00" + strings.ToLower(query.query.Database) + "\x00" + locality + "\x00" + query.fingerprint
}

func sqlStatementOperation(sql string) string {
	fields := strings.Fields(sql)
	if len(fields) == 0 {
		return ""
	}
	return strings.ToUpper(fields[0])
}

func failedState(state string) bool {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "failed", "failure", "error", "panicked", "panic", "bounced":
		return true
	default:
		return false
	}
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
