package http

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	stdlibhttp "net/http"
	"strings"
	"unicode/utf8"

	"github.com/levskiy0/webpprof"
)

// BodyRecorder retains at most a configured prefix of response bytes while
// reporting successful writes to the wrapped response path.
type BodyRecorder struct {
	limit     int64
	body      []byte
	truncated bool
}

// NewBodyRecorder creates a body recorder. Non-positive limits disable body
// retention while preserving byte counts supplied to Message.
func NewBodyRecorder(limit int64) *BodyRecorder {
	return &BodyRecorder{limit: limit}
}

// Write retains data up to the configured limit and always reports the full
// input length so it can be used as a passive tee.
func (c *BodyRecorder) Write(data []byte) (int, error) {
	if c == nil || c.limit <= 0 {
		return len(data), nil
	}
	remaining := c.limit - int64(len(c.body))
	if remaining > 0 {
		count := min(int64(len(data)), remaining)
		c.body = append(c.body, data[:count]...)
	}
	if int64(len(data)) > remaining {
		c.truncated = true
	}
	return len(data), nil
}

// Message builds a safe HTTP message snapshot from retained bytes and headers.
// Unsupported binary bodies are omitted.
func (c *BodyRecorder) Message(headers stdlibhttp.Header, size int64) webpprof.HTTPMessage {
	message := webpprof.HTTPMessage{Headers: headers.Clone(), ContentType: headers.Get("Content-Type"), Size: size}
	if c == nil {
		return message
	}
	message.Body = formatHTTPBody(c.body, message.ContentType)
	message.Truncated = c.truncated
	return message
}

// SnapshotRequest reads and restores up to limit bytes from request.Body and
// returns a redacted message snapshot. Callers must pass a non-nil request.
func SnapshotRequest(request *stdlibhttp.Request, limit int64) webpprof.HTTPMessage {
	message := webpprof.HTTPMessage{Headers: request.Header.Clone(), ContentType: request.Header.Get("Content-Type"), Size: request.ContentLength}
	if limit <= 0 || request.Body == nil {
		return message
	}
	prefix, err := io.ReadAll(io.LimitReader(request.Body, limit+1))
	request.Body = io.NopCloser(io.MultiReader(bytes.NewReader(prefix), request.Body))
	if err != nil {
		return message
	}
	if int64(len(prefix)) > limit {
		prefix = prefix[:limit]
		message.Truncated = true
	}
	message.Body = formatHTTPBody(prefix, message.ContentType)
	return message
}

func formatHTTPBody(body []byte, contentType string) string {
	if len(body) == 0 {
		return ""
	}
	mediaType, _, _ := mime.ParseMediaType(contentType)
	switch {
	case mediaType == "application/json" || strings.HasSuffix(mediaType, "+json"):
		var value any
		if json.Unmarshal(body, &value) == nil {
			webpprof.Redact(value)
			formatted, err := json.MarshalIndent(value, "", "  ")
			if err == nil {
				return string(formatted)
			}
		}
	case mediaType == "application/x-www-form-urlencoded":
		return sanitizeQuery(string(body))
	case strings.HasPrefix(mediaType, "text/"), mediaType == "application/xml", strings.HasSuffix(mediaType, "+xml"), mediaType == "application/javascript":
		if utf8.Valid(body) {
			return string(body)
		}
	}
	return ""
}
