const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");
const vm = require("node:vm");

function loadTimelineFunctions() {
  const applicationPath = path.join(__dirname, "app.js");
  const application = fs.readFileSync(applicationPath, "utf8").replace(
    /\nbind\(\);\nrenderKinds\(\);\nload\(\);\s*$/,
    "\nglobalThis.timelineCriticalPathForTest=timelineCriticalPath;\nglobalThis.operationDurationForTest=operationDuration;",
  );
  const context = {
    document: { getElementById: () => ({}) },
    location: { pathname: "/debug/webpprof/" },
  };
  vm.runInNewContext(application, context, { filename: applicationPath });
  return {
    criticalPath: context.timelineCriticalPathForTest,
    operationDuration: context.operationDurationForTest,
  };
}

test("programmatic task steps participate in bottleneck detection", () => {
  const { criticalPath } = loadTimelineFunctions();
  const startedAt = Date.parse("2026-08-24T08:40:22.824Z");
  const at = (milliseconds) => new Date(startedAt + milliseconds).toISOString();
  const events = [
    { id: "task", kind: "task", started_at: at(0), duration_ns: 1_102_000_000 },
    { id: "load", kind: "event", parent_id: "task", started_at: at(0.1), duration_ns: 1_240_000 },
    { id: "query", kind: "query", parent_id: "load", started_at: at(0.2), duration_ns: 40_000 },
    { id: "render", kind: "event", parent_id: "task", started_at: at(2), duration_ns: 1_100_000_000 },
    { id: "log", kind: "log", parent_id: "render", started_at: at(1_101.4), duration_ns: 0 },
  ];

  const result = criticalPath(events, startedAt, startedAt + 1_102);

  assert.equal(result.bottleneck?.id, "render");
  assert.equal(result.ids.has("render"), true);
  assert.equal(result.ids.has("query"), false);
  assert.ok(result.duration > 1_100);
});

test("measured middleware work excludes downstream time from bottleneck selection", () => {
  const { criticalPath, operationDuration } = loadTimelineFunctions();
  const startedAt = Date.parse("2026-08-24T08:40:22.824Z");
  const at = (milliseconds) => new Date(startedAt + milliseconds).toISOString();
  const events = [
    { id: "request", kind: "request", started_at: at(0), duration_ns: 150_000_000 },
    { id: "middleware", kind: "middleware", parent_id: "request", started_at: at(1), duration_ns: 145_000_000, data: { work_duration_ns: 5_000_000, work_spans: [{ duration_ns: 2_000_000 }, { offset_ns: 142_000_000, duration_ns: 3_000_000 }] } },
    { id: "query", kind: "query", parent_id: "middleware", started_at: at(20), duration_ns: 100_000_000 },
  ];

  const result = criticalPath(events, startedAt, startedAt + 150);

  assert.equal(operationDuration(events[1]), 5_000_000);
  assert.equal(result.bottleneck?.id, "query");
  assert.equal(result.ids.has("middleware"), true);
  assert.equal(result.ids.has("query"), true);
});

test("middleware work segments can participate on both sides of downstream work", () => {
  const { criticalPath } = loadTimelineFunctions();
  const startedAt = Date.parse("2026-08-24T08:40:22.824Z");
  const at = (milliseconds) => new Date(startedAt + milliseconds).toISOString();
  const events = [
    { id: "request", kind: "request", started_at: at(0), duration_ns: 150_000_000 },
    { id: "middleware", kind: "middleware", parent_id: "request", started_at: at(0), duration_ns: 150_000_000, data: { work_duration_ns: 20_000_000, work_spans: [{ duration_ns: 10_000_000 }, { offset_ns: 140_000_000, duration_ns: 10_000_000 }] } },
    { id: "query", kind: "query", parent_id: "middleware", started_at: at(20), duration_ns: 100_000_000 },
  ];

  const result = criticalPath(events, startedAt, startedAt + 150);

  assert.equal(result.duration, 120);
  assert.equal(result.ids.has("middleware"), true);
  assert.equal(result.ids.has("query"), true);
  assert.equal(result.bottleneck?.id, "query");
});

test("short relative maxima are not labeled bottlenecks", () => {
  const { criticalPath } = loadTimelineFunctions();
  const startedAt = Date.parse("2026-08-24T08:40:22.824Z");
  const at = (milliseconds) => new Date(startedAt + milliseconds).toISOString();
  const cases = [
    { kind: "query", durationNS: 40_000_000, windowMS: 60 },
    { kind: "cache", durationNS: 40_000_000, windowMS: 60 },
    { kind: "middleware", durationNS: 90_000_000, windowMS: 120 },
    { kind: "http_call", durationNS: 400_000_000, windowMS: 600 },
    { kind: "event", durationNS: 400_000_000, windowMS: 600 },
  ];

  for(const sample of cases){
    const events = [
      { id: "request", kind: "request", started_at: at(0), duration_ns: sample.windowMS*1_000_000 },
      { id: "operation", kind: sample.kind, parent_id: "request", started_at: at(1), duration_ns: sample.durationNS },
    ];
    const result = criticalPath(events, startedAt, startedAt + sample.windowMS);
    assert.equal(result.bottleneck, null, `${sample.kind} below its absolute threshold`);
  }
});

test("50 ms SQL floor is independent from the 100 ms slow-query finding", () => {
  const { criticalPath } = loadTimelineFunctions();
  const startedAt = Date.parse("2026-08-24T08:40:22.824Z");
  const at = (milliseconds) => new Date(startedAt + milliseconds).toISOString();
  const events = [
    { id: "request", kind: "request", started_at: at(0), duration_ns: 90_000_000 },
    { id: "query", kind: "query", parent_id: "request", started_at: at(5), duration_ns: 60_000_000 },
  ];

  const result = criticalPath(events, startedAt, startedAt + 90);

  assert.equal(result.bottleneck?.id, "query");
});

test("nested slow operation wins over inclusive middleware wrappers", () => {
  const { criticalPath } = loadTimelineFunctions();
  const startedAt = Date.parse("2026-08-24T08:40:22.824Z");
  const at = (milliseconds) => new Date(startedAt + milliseconds).toISOString();
  const events = [
    { id: "request", kind: "request", started_at: at(0), duration_ns: 400_000_000 },
    { id: "outer", kind: "middleware", parent_id: "request", started_at: at(5), duration_ns: 380_000_000, data: {} },
    { id: "inner", kind: "middleware", parent_id: "outer", started_at: at(10), duration_ns: 360_000_000, data: {} },
    { id: "query", kind: "query", parent_id: "inner", started_at: at(50), duration_ns: 250_000_000 },
  ];

  const result = criticalPath(events, startedAt, startedAt + 400);

  assert.equal(result.bottleneck?.id, "query");
});

test("legacy middleware duration remains a compatibility fallback", () => {
  const { operationDuration } = loadTimelineFunctions();

  assert.equal(operationDuration({ kind: "middleware", duration_ns: 42_000_000, data: {} }), 42_000_000);
  assert.equal(operationDuration({ kind: "middleware", duration_ns: 42_000_000, data: { work_duration_ns: 0 } }), 0);
});

test("finding detail and suggestion render on separate rows", () => {
  const application = fs.readFileSync(path.join(__dirname, "app.js"), "utf8");

  assert.match(application, /class="diagnostic-detail"/);
  assert.match(application, /class="diagnostic-suggestion"/);
  assert.doesNotMatch(application, /finding\.detail,finding\.suggestion.*join\(" · "\)/);
});
