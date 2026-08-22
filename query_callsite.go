package webpprof

import (
	"runtime"
	"strings"
)

const callsiteDepth = 16

// CaptureCallsite returns the application stack that led to an operation.
// Integrations may call it before delegating to a dependency so the first
// frame points at application code rather than at the profiler wrapper.
func CaptureCallsite(kind Kind) []SourceFrame {
	programCounters := make([]uintptr, 48)
	count := runtime.Callers(2, programCounters)
	frames := runtime.CallersFrames(programCounters[:count])
	result := make([]SourceFrame, 0, callsiteDepth)
	for {
		frame, more := frames.Next()
		if !isCallsiteInfrastructureFrame(kind, frame.Function) && frame.File != "" && frame.Line > 0 {
			result = append(result, SourceFrame{Function: frame.Function, File: frame.File, Line: frame.Line})
			if len(result) == callsiteDepth {
				break
			}
		}
		if !more {
			break
		}
	}
	return result
}

// CaptureCallsite returns a stack only when automatic capture is enabled for
// kind on this profiler.
func (p *Profiler) CaptureCallsite(kind Kind) []SourceFrame {
	if p == nil || !p.capturesCallsite(kind) {
		return nil
	}
	return CaptureCallsite(kind)
}

// CaptureQueryCallsite is kept as a compatibility alias for SQL integrations.
func CaptureQueryCallsite() []SourceFrame {
	return CaptureCallsite(KindQuery)
}

// CaptureQueryCallsite returns a query stack when query capture is enabled.
func (p *Profiler) CaptureQueryCallsite() []SourceFrame {
	return p.CaptureCallsite(KindQuery)
}

func supportsCallsite(kind Kind) bool {
	switch kind {
	case KindQuery, KindCache, KindEmail, KindJob, KindHTTPCall, KindSchedule:
		return true
	default:
		return false
	}
}

func (p *Profiler) capturesCallsite(kind Kind) bool {
	if p == nil || !supportsCallsite(kind) {
		return false
	}
	_, enabled := p.config.callsiteKinds[kind]
	return enabled
}

func isCallsiteInfrastructureFrame(kind Kind, function string) bool {
	if function == "" || strings.HasPrefix(function, "runtime.") {
		return true
	}
	if marker := strings.Index(function, "github.com/levskiy0/webpprof/profiler/"); marker >= 0 {
		packageFunction := function[marker+len("github.com/levskiy0/webpprof/profiler/"):]
		symbol := packageFunction
		if separator := strings.IndexByte(symbol, '.'); separator >= 0 {
			symbol = symbol[separator+1:]
		}
		lowerSymbol := strings.ToLower(symbol)
		isProfiledReceiver := strings.HasPrefix(lowerSymbol, "(*") && (strings.Contains(lowerSymbol, "profiler") || strings.Contains(lowerSymbol, "profiled"))
		if strings.HasPrefix(lowerSymbol, "profiler") || isProfiledReceiver {
			return true
		}
	}
	if kind == KindQuery && (strings.HasPrefix(function, "database/sql.") || strings.HasPrefix(function, "github.com/uptrace/bun.")) {
		return true
	}
	for _, prefix := range []string{
		"github.com/levskiy0/webpprof.CaptureCallsite",
		"github.com/levskiy0/webpprof.(*Profiler).CaptureCallsite",
		"github.com/levskiy0/webpprof.CaptureQueryCallsite",
		"github.com/levskiy0/webpprof.(*Profiler).CaptureQueryCallsite",
		"github.com/levskiy0/webpprof.(*Profiler).prepareCallsite",
		"github.com/levskiy0/webpprof.(*Profiler).prepareQuery",
		"github.com/levskiy0/webpprof.(*Profiler).Log",
		"github.com/levskiy0/webpprof.Log",
		"github.com/levskiy0/webpprof.withDefault",
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
	p.prepareCallsite(KindQuery, &query.Callsite)
}

func (p *Profiler) prepareCallsite(kind Kind, callsite *[]SourceFrame) {
	if p == nil || callsite == nil {
		return
	}
	if len(*callsite) == 0 && p.capturesCallsite(kind) {
		*callsite = CaptureCallsite(kind)
	}
	if p.config.sourceLink == nil {
		return
	}
	for index := range *callsite {
		if (*callsite)[index].URL == "" {
			(*callsite)[index].URL = p.config.sourceLink((*callsite)[index])
		}
	}
}
