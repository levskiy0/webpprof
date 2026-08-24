---
name: debug-go-with-webpprof
description: Audit and diagnose Go API behavior through application code and the read-only webpprof MCP server. Use for API correctness and performance reviews, request latency regressions, excessive SQL or outbound calls, possible N+1 queries, deciding whether caching is justified, reviewing cache freshness and update paths, failed work, and before/after verification of fixes.
---

# Debug Go With webpprof

Review the API as a data flow, then use webpprof captures as runtime evidence. Explain why each important operation exists, challenge unnecessary work and caching, separate observed facts from hypotheses, and verify fixes under comparable conditions.

## Choose the review mode

Select the narrowest mode that satisfies the request:

- **Trace diagnosis** — use for a slow, failing, or unexpectedly expensive execution. Inspect runtime counts, timing, failures, and only the code paths needed to explain the observed symptom.
- **API and cache audit** — use when the user asks whether the API or caching design is correct. Review the full contract, data lifecycle, alternate write paths, consistency, and cache justification.
- **Fix verification** — use when a change already exists. Compare equivalent before and after captures, run relevant tests, and verify the stated correctness and performance targets.

Do not combine modes automatically. Start with trace diagnosis when the request is ambiguous, then expand only when the user requests a broader review or runtime evidence points to a contract, data-lifecycle, or cache-design problem.

## Understand the target

For trace diagnosis, establish the endpoint or execution, expected outcome, observed symptom, and the relevant read or write path. Do not require an exhaustive API contract or every mutation path unless they are needed to explain the symptom.

For an API and cache audit, inspect the route and relevant code before making architectural recommendations. Establish:

- The API's purpose, callers, method, route, authentication, authorization, inputs, outputs, status codes, errors, pagination, idempotency, and side effects.
- The expected latency, consistency, and data-freshness requirements. Ask for them only when they materially change the recommendation and cannot be inferred.
- The complete read path from handler through services to databases, caches, and remote systems.
- The source of truth for every important response field.
- Every write path that can change those values, including API mutations, admin actions, jobs, schedules, message consumers, webhooks, imports, and direct storage changes supported by the application.
- The expected number of SQL, cache, and outbound operations for one logical request.

For fix verification, establish the original symptom, changed behavior, comparable reproduction, and measurable acceptance criteria.

In API and cache audit mode, do not limit the review to operations visible in one trace. Use runtime evidence to locate relevant code, then inspect the codebase for alternate reads, writes, invalidation paths, and failure handling.

## Choose the capture workflow

Use the configured `webpprof_*` MCP tools. If they are unavailable, stop and explain that the webpprof MCP server must be configured; do not invent profiler results.

For a new reproduction:

1. Call `webpprof_status` and record the current cursor, capture settings, sampling, retention, and disabled event kinds.
2. Reproduce the operation once using an existing safe command or ask the user to reproduce it when doing so would mutate data or needs credentials.
3. Call `webpprof_wait_for_request` with the recorded cursor and the narrowest known method, path, status, and duration filters.
4. Inspect the returned request with `webpprof_inspect_request`.

For an existing capture:

1. Call `webpprof_status`.
2. Use `webpprof_list_requests` with route, method, status, duration, or tag filters.
3. Select captures matching the reported scenario, not merely the slowest unrelated request.
4. Inspect the selected request with `webpprof_inspect_request`.

For a schedule, callable, or task, locate the root with `webpprof_search_events`, then use `webpprof_inspect_event` and restrict follow-up searches with `scope_id`.

## Validate capture quality

Before drawing conclusions:

- Confirm the method, route or path, status, tags, and execution identity.
- Check `has_more`. If the timeline is truncated, re-inspect with a larger `max_events` or use targeted `webpprof_search_events` calls.
- Treat disabled event kinds, sampling, expired retention, missing context propagation, and truncated timelines as evidence gaps.
- Keep `include_payloads` false unless payloads or stacks are necessary and the data is safe to reveal.
- Treat `counts` from an inspection as counts of correlated events by kind. Do not present them as the total number of incoming HTTP requests.

## Analyze the execution

Start with automatic `findings`, but verify every important conclusion against event counts and the timeline.

### API behavior

Apply the complete checklist in API and cache audit mode. In trace diagnosis, inspect only the behavior needed to explain the reported symptom.

- Verify that the observed status, errors, and side effects match the endpoint contract.
- Check input validation, authentication and object-level authorization in code. Do not expose captured credentials or sensitive payloads.
- Check whether reads and writes use appropriate transaction boundaries and whether partial failure can leave inconsistent state.
- Challenge every query, cache lookup, remote call, retry, and background dispatch: connect it to a required response field or side effect, or identify it as removable work.
- Check pagination and bounded result sizes for collection endpoints.
- Check timeout propagation, cancellation, retry safety, and idempotency for remote calls and mutations.

### Request and timing

- Record method, route or path, status, total duration, and capture identity.
- Identify the longest operations and the operations on the critical path.
- Distinguish inclusive middleware timing from exclusive work.
- Do not add overlapping child durations and present the sum as request wall time.
- Compare against a user-provided SLA or a comparable baseline. Do not copy webpprof's internal finding thresholds into the diagnosis as product requirements.

### SQL

- Report query count, slowest queries, returned row counts when captured, and approximate SQL wall-clock share.
- Group structurally equivalent queries and verify possible N+1 behavior.
- Distinguish repeated queries caused by a cache miss from unrelated duplicate queries.
- Inspect callsites and query plans when available before recommending an index or rewrite.

### Cache

Apply the complete cache review in API and cache audit mode, or when trace evidence identifies caching as relevant. Otherwise report the observed cache operations without expanding the scope.

Do not assume an existing cache is desirable. Reach an explicit `keep`, `redesign`, or `remove` conclusion.

1. Decide whether caching is justified:
   - Measure the uncached operation's latency and resource cost.
   - Estimate request frequency, read/write ratio, reuse across requests, and origin-system capacity from available evidence.
   - Compare the benefit with invalidation complexity, memory use, operational failure modes, and correctness risk.
   - Prefer the source of truth directly when the operation is already cheap, data changes frequently, reuse is low, or freshness must be strict.
2. Define the correctness model:
   - Identify the source of truth and the maximum acceptable staleness.
   - Verify that keys include every dimension affecting the value, such as tenant, user, locale, permissions, version, and query parameters.
   - Check whether negative results may be cached and for how long.
3. Trace how cached data changes:
   - Find every mutation path for the underlying data.
   - Identify cache-aside, read-through, write-through, write-behind, event-driven invalidation, or TTL-only behavior.
   - Verify update ordering, invalidation on success, transaction boundaries, concurrent writers, partial failures, and retry behavior.
   - Check stampede protection, cold-start behavior, fallback when the cache is unavailable, and whether stale data may be served intentionally.
4. Validate runtime behavior:
   - Count successful reads, hits, misses, writes, deletes, and errors.
   - Calculate hit rate only from comparable successful reads and state the sample size.
   - Check repeated keys, key cardinality, TTL, miss-followed-by-load-and-populate behavior, and repeated loads of the same resource.
   - Compare cold and warm requests. Only in an explicitly authorized local, test, or disposable environment, perform a controlled source-data mutation and verify that the next read satisfies the promised freshness.

Never mutate production or shared data for diagnosis. When controlled mutation is not authorized, use existing safe tests or captures, or provide an exact verification procedure for the user. Never infer cache correctness or usefulness from a single request or from hit rate alone.

### Outbound and supporting work

- Inspect outbound HTTP or gRPC count, latency, status, errors, timeouts, retries, and sequential calls that may safely run concurrently.
- Inspect slow middleware, failed jobs, mail, messaging, schedules, callables, tasks, exceptions, and error logs.
- Follow `parent_id`, `request_id`, and `origin_request_id` carefully. Do not charge asynchronously correlated work to request latency unless it lies on the request's execution path.

Use `webpprof_search_events` only to obtain missing focused evidence, such as all queries for one request, cache operations for a key, or calls above a duration threshold.

## Compare multiple samples

When reproduction is safe and deterministic, collect at least three comparable captures. Keep route, input, authentication context, dataset, and cache state consistent.

Report distributions or ranges instead of overclaiming from one outlier. Label cold-cache and warm-cache captures separately. If only one capture exists, state that limitation explicitly.

## Inspect and fix code

When the user asks for a fix:

1. Use entry callsites, SQL, cache keys, operation names, and route information to locate the relevant code.
2. Form the narrowest hypothesis supported by profiler evidence.
3. Inspect the implementation and tests before editing.
4. Make only the authorized code change.
5. Run relevant tests.
6. Repeat the same capture scenario and compare before and after results.

Do not claim a performance fix solely because tests pass. Require a comparable post-change capture when the application can be run locally; otherwise provide an exact verification procedure.

## Report the diagnosis

Include only the sections relevant to the selected mode and use this order:

1. **Conclusion** — lead with the outcome, the most likely cause, the strongest supporting measurement, and confidence in one concise paragraph.
2. **API contract and data flow** — state what the endpoint must do, its source of truth, and how the data changes.
3. **Observation** — identify incorrect, slow, or unexpectedly frequent work.
4. **Evidence** — include request IDs, event IDs, counts, durations, cache sample sizes, relevant code paths, and findings.
5. **Likely cause** — clearly label inference and confidence.
6. **Cache decision** — state `keep`, `redesign`, `remove`, or `not applicable`, with freshness and update semantics.
7. **Recommended change** — connect the change to API requirements and evidence.
8. **Verification** — compare request counts and latency before and after; test correctness after controlled data mutation only in an explicitly authorized local, test, or disposable environment, otherwise provide the procedure.
9. **Limitations** — note truncation, sampling, missing event kinds, unsafe payload access, or insufficient samples.

Prefer concrete comparisons such as “12 queries to 2” or “420 ms to 160 ms” over qualitative claims. Never fabricate missing measurements.
