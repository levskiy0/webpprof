const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");
const vm = require("node:vm");

function loadTimelineCriticalPath() {
  const applicationPath = path.join(__dirname, "app.js");
  const application = fs.readFileSync(applicationPath, "utf8").replace(
    /\nbind\(\);\nrenderKinds\(\);\nload\(\);\s*$/,
    "\nglobalThis.timelineCriticalPathForTest=timelineCriticalPath;",
  );
  const context = {
    document: { getElementById: () => ({}) },
    location: { pathname: "/debug/webpprof/" },
  };
  vm.runInNewContext(application, context, { filename: applicationPath });
  return context.timelineCriticalPathForTest;
}

test("programmatic task steps participate in bottleneck detection", () => {
  const criticalPath = loadTimelineCriticalPath();
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

test("finding detail and suggestion render on separate rows", () => {
  const application = fs.readFileSync(path.join(__dirname, "app.js"), "utf8");

  assert.match(application, /class="diagnostic-detail"/);
  assert.match(application, /class="diagnostic-suggestion"/);
  assert.doesNotMatch(application, /finding\.detail,finding\.suggestion.*join\(" · "\)/);
});
