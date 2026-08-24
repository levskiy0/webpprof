package webpprof

import (
	"regexp"
	"strconv"
	"strings"
)

const queryPlanLargeEstimateMinimum = int64(10_000)

var (
	postgresSequentialScanPattern = regexp.MustCompile(`(?i)\bseq scan on\s+([\w.\"` + "`" + `]+)`)
	postgresRowsPattern           = regexp.MustCompile(`(?i)\brows=(\d+)`)
	sqliteScanPattern             = regexp.MustCompile(`(?i)\bscan\s+(?:table\s+)?([\w.\"` + "`" + `]+)`)
	mysqlRowsPattern              = regexp.MustCompile(`(?i)(?:^|\s)rows=(\d+)(?:\s|$)`)
)

// DetectQueryPlanIssues normalizes conservative performance signals from a
// plain-text EXPLAIN plan. It never executes the statement and returns no issue
// for plan shapes it cannot recognize safely.
func DetectQueryPlanIssues(driverName, planText string) []QueryPlanIssue {
	driverName = strings.ToLower(strings.TrimSpace(driverName))
	issues := make([]QueryPlanIssue, 0)
	seen := make(map[string]struct{})
	add := func(issue QueryPlanIssue) {
		key := string(issue.Code) + "\x00" + issue.Relation + "\x00" + issue.Detail
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		issues = append(issues, issue)
	}

	for _, rawLine := range strings.Split(planText, "\n") {
		line := strings.TrimSpace(rawLine)
		lower := strings.ToLower(line)
		if line == "" {
			continue
		}
		switch driverName {
		case "sqlite", "sqlite3":
			if match := sqliteScanPattern.FindStringSubmatch(line); len(match) == 2 &&
				!strings.Contains(lower, "using index") && !strings.Contains(lower, "using covering index") {
				add(QueryPlanIssue{Code: QueryPlanIssueFullScan, Relation: trimPlanIdentifier(match[1]), Detail: line})
			}
			if strings.Contains(lower, "use temp b-tree") {
				add(QueryPlanIssue{Code: QueryPlanIssueTemporarySort, Detail: line})
			}
		case "postgres", "postgresql", "pgx":
			if match := postgresSequentialScanPattern.FindStringSubmatch(line); len(match) == 2 {
				add(QueryPlanIssue{Code: QueryPlanIssueFullScan, Relation: trimPlanIdentifier(match[1]), Detail: line})
			}
			if strings.Contains(lower, "sort method:") && (strings.Contains(lower, "disk") || strings.Contains(lower, "external")) {
				add(QueryPlanIssue{Code: QueryPlanIssueTemporarySort, Detail: line})
			}
			addLargePlanEstimate(&issues, seen, postgresRowsPattern.FindAllStringSubmatch(line, -1), line)
		case "mysql", "mariadb":
			if strings.Contains(lower, "type=all") {
				add(QueryPlanIssue{Code: QueryPlanIssueFullScan, Relation: planField(line, "table"), Detail: line})
			}
			if strings.Contains(lower, "using filesort") || strings.Contains(lower, "using temporary") {
				add(QueryPlanIssue{Code: QueryPlanIssueTemporarySort, Detail: line})
			}
			addLargePlanEstimate(&issues, seen, mysqlRowsPattern.FindAllStringSubmatch(line, -1), line)
		}
	}
	return issues
}

func addLargePlanEstimate(issues *[]QueryPlanIssue, seen map[string]struct{}, matches [][]string, detail string) {
	for _, match := range matches {
		if len(match) != 2 {
			continue
		}
		rows, err := strconv.ParseInt(match[1], 10, 64)
		if err != nil || rows < queryPlanLargeEstimateMinimum {
			continue
		}
		key := string(QueryPlanIssueLargeEstimate) + "\x00" + strconv.FormatInt(rows, 10)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		*issues = append(*issues, QueryPlanIssue{Code: QueryPlanIssueLargeEstimate, EstimatedRows: rows, Detail: detail})
	}
}

func trimPlanIdentifier(value string) string {
	return strings.Trim(value, "`\"")
}

func planField(line, name string) string {
	prefix := strings.ToLower(name) + "="
	for _, field := range strings.Fields(line) {
		if strings.HasPrefix(strings.ToLower(field), prefix) {
			return trimPlanIdentifier(field[len(prefix):])
		}
	}
	return ""
}
