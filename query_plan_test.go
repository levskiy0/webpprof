package webpprof

import "testing"

func TestDetectQueryPlanIssues(t *testing.T) {
	tests := []struct {
		name   string
		driver string
		plan   string
		codes  []QueryPlanIssueCode
	}{
		{name: "sqlite scan", driver: "sqlite", plan: "id=2 parent=0 detail=SCAN players", codes: []QueryPlanIssueCode{QueryPlanIssueFullScan}},
		{name: "sqlite indexed search", driver: "sqlite", plan: "id=2 parent=0 detail=SEARCH players USING INTEGER PRIMARY KEY (rowid=?)"},
		{name: "sqlite temporary sort", driver: "sqlite", plan: "id=4 parent=0 detail=USE TEMP B-TREE FOR ORDER BY", codes: []QueryPlanIssueCode{QueryPlanIssueTemporarySort}},
		{name: "postgres scan and estimate", driver: "pgx", plan: "Seq Scan on audit_log  (cost=0.00..2000.00 rows=50000 width=32)", codes: []QueryPlanIssueCode{QueryPlanIssueFullScan, QueryPlanIssueLargeEstimate}},
		{name: "mysql full scan and filesort", driver: "mysql", plan: "table=players  type=ALL  rows=12000  Extra=Using filesort", codes: []QueryPlanIssueCode{QueryPlanIssueFullScan, QueryPlanIssueTemporarySort, QueryPlanIssueLargeEstimate}},
		{name: "unknown driver", driver: "custom", plan: "Seq Scan on players"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			issues := DetectQueryPlanIssues(test.driver, test.plan)
			if len(issues) != len(test.codes) {
				t.Fatalf("issues = %+v, want codes %v", issues, test.codes)
			}
			for index, code := range test.codes {
				if issues[index].Code != code {
					t.Fatalf("issue[%d] = %+v, want %q", index, issues[index], code)
				}
			}
		})
	}
}
