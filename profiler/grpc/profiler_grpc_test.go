package grpc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/levskiy0/webpprof"
	googlegrpc "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestUnaryServerInterceptorCorrelatesNestedOperations(t *testing.T) {
	mux := http.NewServeMux()
	profiler := webpprof.New(mux, webpprof.WithUnsafeUnauthenticatedAccess())
	t.Cleanup(func() { _ = profiler.Close() })

	interceptor := UnaryServerInterceptorWith(profiler)
	_, err := interceptor(context.Background(), "request", &googlegrpc.UnaryServerInfo{FullMethod: "/players.PlayerService/Get"}, func(ctx context.Context, _ any) (any, error) {
		profiler.LogEventContext(ctx, webpprof.Event{Kind: "player", Name: "loaded"})
		return nil, status.Error(codes.NotFound, "missing")
	})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("status = %v", err)
	}

	entries := readEntries(t, mux)
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want request and event", len(entries))
	}
	var request webpprof.Request
	var related webpprof.Entry
	for _, entry := range entries {
		if entry.Kind == webpprof.KindRequest {
			if err := json.Unmarshal(entry.Data, &request); err != nil {
				t.Fatal(err)
			}
		} else if entry.Kind == webpprof.KindEvent {
			related = entry
		}
	}
	if request.Route != "/players.PlayerService/Get" || request.Status != 404 {
		t.Fatalf("request = %#v", request)
	}
	if related.RequestID != request.ID {
		t.Fatalf("event request ID = %q, want %q", related.RequestID, request.ID)
	}
}

func TestUnaryClientInterceptorRecordsOutgoingCall(t *testing.T) {
	mux := http.NewServeMux()
	profiler := webpprof.New(mux, webpprof.WithUnsafeUnauthenticatedAccess())
	t.Cleanup(func() { _ = profiler.Close() })

	interceptor := UnaryClientInterceptorWith(profiler)
	err := interceptor(context.Background(), "/billing.Billing/Charge", nil, nil, nil, func(context.Context, string, any, any, *googlegrpc.ClientConn, ...googlegrpc.CallOption) error {
		return status.Error(codes.Unavailable, "offline")
	})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("status = %v", err)
	}

	entries := readEntries(t, mux)
	if len(entries) != 1 || entries[0].Kind != webpprof.KindHTTPCall {
		t.Fatalf("entries = %#v", entries)
	}
	var call webpprof.HTTPCall
	if err := json.Unmarshal(entries[0].Data, &call); err != nil {
		t.Fatal(err)
	}
	if call.Method != "GRPC" || call.URL != "/billing.Billing/Charge" || call.Status != 503 || call.Error == "" {
		t.Fatalf("call = %#v", call)
	}
}

func TestUnaryServerInterceptorHonorsRequestSampling(t *testing.T) {
	mux := http.NewServeMux()
	profiler := webpprof.New(mux, webpprof.WithUnsafeUnauthenticatedAccess(), webpprof.WithRequestSampleRate(0))
	t.Cleanup(func() { _ = profiler.Close() })

	interceptor := UnaryServerInterceptorWith(profiler)
	_, err := interceptor(context.Background(), nil, &googlegrpc.UnaryServerInfo{FullMethod: "/players.PlayerService/Get"}, func(ctx context.Context, _ any) (any, error) {
		if webpprof.RecordingEnabled(ctx) {
			t.Fatal("handler context should disable downstream recording")
		}
		profiler.LogEventContext(ctx, webpprof.Event{Kind: "player", Name: "loaded"})
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if entries := readEntries(t, mux); len(entries) != 0 {
		t.Fatalf("entries = %#v", entries)
	}
}

func readEntries(t *testing.T, handler http.Handler) []webpprof.Entry {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/debug/webpprof/api/events?limit=20", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	var result struct {
		Events []webpprof.Entry `json:"events"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result.Events
}
