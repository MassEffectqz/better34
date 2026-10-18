package internal

import (
	"os"
	"strings"
)

// GetEnv возвращает значение переменной окружения с префиксом BETTER34_
// или fallback на старый префикс BRIEFLY_.
func GetEnv(key string) string {
	var b34Key, brieflyKey string
	if strings.HasPrefix(key, "BETTER34_") {
		b34Key = key
		brieflyKey = "BRIEFLY_" + strings.TrimPrefix(key, "BETTER34_")
	} else if strings.HasPrefix(key, "BRIEFLY_") {
		brieflyKey = key
		b34Key = "BETTER34_" + strings.TrimPrefix(key, "BRIEFLY_")
	} else {
		b34Key = "BETTER34_" + key
		brieflyKey = "BRIEFLY_" + key
	}
	if v := strings.TrimSpace(os.Getenv(b34Key)); v != "" {
		return v
	}
	return strings.TrimSpace(os.Getenv(brieflyKey))
}
