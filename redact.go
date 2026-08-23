package webpprof

import (
	"encoding/json"
	"strings"
)

var sensitiveKeys = []string{"authorization", "cookie", "password", "passwd", "token", "secret", "api_key", "apikey", "jwt", "dsn"}

func marshalRedacted(value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var document any
	if err := json.Unmarshal(encoded, &document); err != nil {
		return nil, err
	}
	redact(document)
	return json.Marshal(document)
}

func redact(value any) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if isSensitiveKey(key) {
				typed[key] = redactedValue(child)
				continue
			}
			redact(child)
		}
	case []any:
		for _, child := range typed {
			redact(child)
		}
	}
}

func redactedValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			typed[key] = redactedValue(child)
		}
		return typed
	case []any:
		for index, child := range typed {
			typed[index] = redactedValue(child)
		}
		return typed
	case nil:
		return nil
	default:
		return "[REDACTED]"
	}
}

// Redact replaces sensitive values in JSON-like maps and slices in place.
// Structs and other concrete values are left unchanged; Log methods perform a
// JSON round trip before applying the same policy.
func Redact(value any) {
	redact(value)
}

func isSensitiveKey(key string) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	normalized = strings.NewReplacer("-", "_", ".", "_").Replace(normalized)
	for _, sensitive := range sensitiveKeys {
		if normalized == sensitive || strings.HasSuffix(normalized, "_"+sensitive) {
			return true
		}
	}
	return false
}

// IsSensitiveKey reports whether key matches the profiler's built-in secret
// names after case and separator normalization.
func IsSensitiveKey(key string) bool {
	return isSensitiveKey(key)
}
