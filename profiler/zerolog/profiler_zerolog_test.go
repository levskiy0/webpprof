package zerolog

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/levskiy0/webpprof"
	"github.com/rs/zerolog"
)

func TestProfilePreservesOutputAndRecordsContext(t *testing.T) {
	mux := http.NewServeMux()
	profiler := webpprof.New(mux)
	t.Cleanup(func() { _ = profiler.Close() })

	var output bytes.Buffer
	logger := ProfileWith(profiler, zerolog.New(&output))
	ctx := webpprof.WithTags(context.Background(), map[string]string{"tenant": "acme"})
	logger.Info().Ctx(ctx).Str("player", "42").Msg("loaded")

	if output.Len() == 0 {
		t.Fatal("zerolog output was not preserved")
	}
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/debug/webpprof/api/events?kind=log&limit=10", nil))
	var payload struct {
		Events []webpprof.Entry `json:"events"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Events) != 1 {
		t.Fatalf("events = %#v", payload.Events)
	}
	var logged webpprof.Log
	if err := json.Unmarshal(payload.Events[0].Data, &logged); err != nil {
		t.Fatal(err)
	}
	if logged.Level != "info" || logged.Message != "loaded" || logged.Tags["tenant"] != "acme" {
		t.Fatalf("log = %#v", logged)
	}
}
