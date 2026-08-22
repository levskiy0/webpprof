package webpprof

import (
	"context"
	"sync"
	"time"
)

type QuerySpan struct {
	ctx   context.Context
	query Query
	once  sync.Once
}

func StartQuery(ctx context.Context, query Query) *QuerySpan {
	if query.StartedAt.IsZero() {
		query.StartedAt = time.Now().UTC()
	}
	return &QuerySpan{ctx: ctx, query: query}
}

func (s *QuerySpan) Finish(err error) {
	s.finish(nil, err)
}

func (s *QuerySpan) FinishRows(rowsAffected int64, err error) {
	s.finish(int64Pointer(rowsAffected), err)
}

func (s *QuerySpan) finish(rowsAffected *int64, err error) {
	if s == nil {
		return
	}
	s.once.Do(func() {
		s.query.Duration = time.Since(s.query.StartedAt)
		s.query.RowsAffected = rowsAffected
		if err != nil {
			s.query.Error = err.Error()
		}
		LogQueryContext(s.ctx, s.query)
	})
}

func int64Pointer(value int64) *int64 {
	return &value
}
