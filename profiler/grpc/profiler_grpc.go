// Package grpc provides server and client interceptors for profiling gRPC
// calls without adding the gRPC dependency to the webpprof core module.
package grpc

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/levskiy0/webpprof"
	googlegrpc "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// UnaryServerInterceptor profiles unary server calls as webpprof requests.
func UnaryServerInterceptor() googlegrpc.UnaryServerInterceptor {
	return UnaryServerInterceptorWith(webpprof.Default())
}

// UnaryServerInterceptorWith profiles unary server calls with p.
func UnaryServerInterceptorWith(p *webpprof.Profiler) googlegrpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *googlegrpc.UnaryServerInfo, handler googlegrpc.UnaryHandler) (response any, err error) {
		if p == nil || !webpprof.RecordingEnabled(ctx) {
			return handler(ctx, req)
		}
		if !shouldCapture(p, info.FullMethod) {
			return handler(webpprof.WithoutRecording(ctx), req)
		}

		capture := p.BeginRequest(serverRequest(info.FullMethod, "unary"))
		ctx = webpprof.WithRequest(ctx, capture)
		defer func() {
			if recovered := recover(); recovered != nil {
				p.LogExceptionContext(ctx, webpprof.PanicException(recovered))
				capture.Finish(webpprof.RequestResult{Status: grpcStatusCode(codes.Internal), Error: fmt.Sprint(recovered)})
				panic(recovered)
			}
		}()

		response, err = handler(ctx, req)
		capture.Finish(requestResult(err))
		return response, err
	}
}

// StreamServerInterceptor profiles streaming server calls as webpprof requests.
func StreamServerInterceptor() googlegrpc.StreamServerInterceptor {
	return StreamServerInterceptorWith(webpprof.Default())
}

// StreamServerInterceptorWith profiles streaming server calls with p.
func StreamServerInterceptorWith(p *webpprof.Profiler) googlegrpc.StreamServerInterceptor {
	return func(srv any, stream googlegrpc.ServerStream, info *googlegrpc.StreamServerInfo, handler googlegrpc.StreamHandler) (err error) {
		ctx := stream.Context()
		if p == nil || !webpprof.RecordingEnabled(ctx) {
			return handler(srv, stream)
		}
		if !shouldCapture(p, info.FullMethod) {
			return handler(srv, &serverStream{ServerStream: stream, ctx: webpprof.WithoutRecording(ctx)})
		}

		capture := p.BeginRequest(serverRequest(info.FullMethod, "stream"))
		ctx = webpprof.WithRequest(ctx, capture)
		profiled := &serverStream{ServerStream: stream, ctx: ctx}
		defer func() {
			if recovered := recover(); recovered != nil {
				p.LogExceptionContext(ctx, webpprof.PanicException(recovered))
				capture.Finish(webpprof.RequestResult{Status: grpcStatusCode(codes.Internal), Error: fmt.Sprint(recovered)})
				panic(recovered)
			}
		}()

		err = handler(srv, profiled)
		capture.Finish(requestResult(err))
		return err
	}
}

// UnaryClientInterceptor profiles unary client calls as outgoing HTTP-call
// entities. The Method value is GRPC and URL contains the full RPC method.
func UnaryClientInterceptor() googlegrpc.UnaryClientInterceptor {
	return UnaryClientInterceptorWith(webpprof.Default())
}

// UnaryClientInterceptorWith profiles unary client calls with p.
func UnaryClientInterceptorWith(p *webpprof.Profiler) googlegrpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, connection *googlegrpc.ClientConn, invoker googlegrpc.UnaryInvoker, options ...googlegrpc.CallOption) error {
		if p == nil || !webpprof.RecordingEnabled(ctx) {
			return invoker(ctx, method, req, reply, connection, options...)
		}

		startedAt := time.Now().UTC()
		callsite := p.CaptureCallsite(webpprof.KindHTTPCall)
		err := invoker(ctx, method, req, reply, connection, options...)
		p.LogHTTPCallContext(ctx, clientCall(method, "unary", startedAt, callsite, err))
		return err
	}
}

// StreamClientInterceptor profiles a streaming client call when the stream
// finishes, or immediately when stream creation fails.
func StreamClientInterceptor() googlegrpc.StreamClientInterceptor {
	return StreamClientInterceptorWith(webpprof.Default())
}

// StreamClientInterceptorWith profiles streaming client calls with p.
func StreamClientInterceptorWith(p *webpprof.Profiler) googlegrpc.StreamClientInterceptor {
	return func(ctx context.Context, description *googlegrpc.StreamDesc, connection *googlegrpc.ClientConn, method string, streamer googlegrpc.Streamer, options ...googlegrpc.CallOption) (googlegrpc.ClientStream, error) {
		if p == nil || !webpprof.RecordingEnabled(ctx) {
			return streamer(ctx, description, connection, method, options...)
		}

		profiled := &clientStreamCapture{
			ctx:       ctx,
			profiler:  p,
			method:    method,
			startedAt: time.Now().UTC(),
			callsite:  p.CaptureCallsite(webpprof.KindHTTPCall),
		}
		stream, err := streamer(ctx, description, connection, method, options...)
		if err != nil {
			profiled.finish(err)
			return nil, err
		}
		profiled.ClientStream = stream
		return profiled, nil
	}
}

type serverStream struct {
	googlegrpc.ServerStream
	ctx context.Context
}

func (s *serverStream) Context() context.Context { return s.ctx }

type clientStreamCapture struct {
	googlegrpc.ClientStream
	ctx       context.Context
	profiler  *webpprof.Profiler
	method    string
	startedAt time.Time
	callsite  []webpprof.SourceFrame
	once      sync.Once
}

func (s *clientStreamCapture) RecvMsg(message any) error {
	err := s.ClientStream.RecvMsg(message)
	if err != nil {
		s.finish(err)
	}
	return err
}

func (s *clientStreamCapture) CloseSend() error {
	err := s.ClientStream.CloseSend()
	if err != nil {
		s.finish(err)
	}
	return err
}

func (s *clientStreamCapture) finish(err error) {
	s.once.Do(func() {
		if err == io.EOF {
			err = nil
		}
		s.profiler.LogHTTPCallContext(s.ctx, clientCall(s.method, "stream", s.startedAt, s.callsite, err))
	})
}

func serverRequest(method, rpcType string) webpprof.Request {
	return webpprof.Request{
		Meta: webpprof.Meta{
			StartedAt: time.Now().UTC(),
			Tags: map[string]string{
				"rpc.system": "grpc",
				"rpc.side":   "server",
				"rpc.type":   rpcType,
			},
		},
		Method:   "GRPC",
		Path:     method,
		Route:    method,
		Protocol: "gRPC",
	}
}

func shouldCapture(p *webpprof.Profiler, method string) bool {
	return p.ShouldCaptureRequest(&http.Request{Method: "GRPC", URL: &url.URL{Path: method}})
}

func requestResult(err error) webpprof.RequestResult {
	return webpprof.RequestResult{Status: grpcStatusCode(status.Code(err)), Error: errorString(err)}
}

func clientCall(method, rpcType string, startedAt time.Time, callsite []webpprof.SourceFrame, err error) webpprof.HTTPCall {
	return webpprof.HTTPCall{
		Meta: webpprof.Meta{
			StartedAt: startedAt,
			Duration:  time.Since(startedAt),
			Tags: map[string]string{
				"rpc.system": "grpc",
				"rpc.side":   "client",
				"rpc.type":   rpcType,
			},
		},
		Method:   "GRPC",
		URL:      method,
		Status:   grpcStatusCode(status.Code(err)),
		Callsite: callsite,
		Error:    errorString(err),
	}
}

func errorString(err error) string {
	if err == nil || err == io.EOF {
		return ""
	}
	return err.Error()
}

func grpcStatusCode(code codes.Code) int {
	switch code {
	case codes.OK:
		return 200
	case codes.InvalidArgument, codes.FailedPrecondition, codes.OutOfRange:
		return 400
	case codes.Unauthenticated:
		return 401
	case codes.PermissionDenied:
		return 403
	case codes.NotFound:
		return 404
	case codes.AlreadyExists, codes.Aborted:
		return 409
	case codes.ResourceExhausted:
		return 429
	case codes.Canceled:
		return 499
	case codes.Unimplemented:
		return 501
	case codes.Unavailable:
		return 503
	case codes.DeadlineExceeded:
		return 504
	default:
		return 500
	}
}

// ServiceMethod splits a full gRPC method into service and method names.
func ServiceMethod(fullMethod string) (service, method string) {
	trimmed := strings.TrimPrefix(fullMethod, "/")
	separator := strings.LastIndexByte(trimmed, '/')
	if separator < 0 {
		return "", trimmed
	}
	return trimmed[:separator], trimmed[separator+1:]
}
