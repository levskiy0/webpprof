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
				typed[key] = "[REDACTED]"
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

func IsSensitiveKey(key string) bool {
	return isSensitiveKey(key)
}
