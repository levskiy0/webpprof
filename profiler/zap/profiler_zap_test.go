package zap

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/levskiy0/webpprof"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func TestProfilePreservesCoreAndRecordsFields(t *testing.T) {
	mux := http.NewServeMux()
	profiler := webpprof.New(mux)
	t.Cleanup(func() { _ = profiler.Close() })
	core := zapcore.NewCore(zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()), zapcore.AddSync(io.Discard), zapcore.DebugLevel)
	profiled := ProfileWith(profiler, core)
	if ProfileWith(profiler, profiled) != profiled {
		t.Fatal("core was wrapped twice")
	}
	zap.New(profiled).With(zap.String("component", "worker")).Warn("retrying", zap.Int("attempt", 2))
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/debug/webpprof/api/events?kind=log&limit=10", nil))
	var response struct {
		Events []webpprof.Entry `json:"events"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Events) != 1 {
		t.Fatalf("events = %+v", response.Events)
	}
	var event webpprof.Log
	if err := json.Unmarshal(response.Events[0].Data, &event); err != nil {
		t.Fatal(err)
	}
	if event.Level != "warn" || event.Message != "retrying" || event.Fields["component"] != "worker" || event.Fields["attempt"] != float64(2) {
		t.Fatalf("log = %+v", event)
	}
}
