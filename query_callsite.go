package webpprof

import (
	"runtime"
	"strings"
)

const queryCallsiteDepth = 16

// CaptureQueryCallsite returns the application stack that led to the current
// query. SQL integrations call this before delegating to the database so the
// first frame points at application code rather than at the profiler wrapper.
func CaptureQueryCallsite() []SourceFrame {
	programCounters := make([]uintptr, 48)
	count := runtime.Callers(2, programCounters)
	frames := runtime.CallersFrames(programCounters[:count])
	result := make([]SourceFrame, 0, queryCallsiteDepth)
	for {
		frame, more := frames.Next()
		if !isQueryInfrastructureFrame(frame.Function) && frame.File != "" && frame.Line > 0 {
			result = append(result, SourceFrame{Function: frame.Function, File: frame.File, Line: frame.Line})
			if len(result) == queryCallsiteDepth {
				break
			}
		}
		if !more {
			break
		}
	}
	return result
}

func isQueryInfrastructureFrame(function string) bool {
	if function == "" || strings.HasPrefix(function, "runtime.") || strings.HasPrefix(function, "database/sql.") {
		return true
	}
	for _, prefix := range []string{
		"github.com/levskiy0/webpprof.CaptureQueryCallsite",
		"github.com/levskiy0/webpprof.(*Profiler).prepareQuery",
		"github.com/levskiy0/webpprof.(*Profiler).LogQuery",
		"github.com/levskiy0/webpprof.(*Profiler).LogQueryContext",
		"github.com/levskiy0/webpprof.LogQuery",
		"github.com/levskiy0/webpprof.LogQueryContext",
		"github.com/levskiy0/webpprof/profiler/sql.",
		"github.com/levskiy0/webpprof/profiler/bun.",
		"github.com/uptrace/bun.",
	} {
		if strings.HasPrefix(function, prefix) {
			return true
		}
	}
	return false
}

func (p *Profiler) prepareQuery(query *Query) {
	if query == nil {
		return
	}
	if len(query.Callsite) == 0 && p.config.queryCallsite {
		query.Callsite = CaptureQueryCallsite()
	}
	if p.config.sourceLink == nil {
		return
	}
	for index := range query.Callsite {
		if query.Callsite[index].URL == "" {
			query.Callsite[index].URL = p.config.sourceLink(query.Callsite[index])
		}
	}
}
