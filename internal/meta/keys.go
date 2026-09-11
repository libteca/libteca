package meta

import (
	"os"
	"strings"
)

var keyLookup func(key string) (string, bool)

func SetKeyLookup(f func(key string) (string, bool)) {
	keyLookup = f
}

func settingKey(env string) string {
	s := strings.TrimPrefix(env, "LIBTECA_")
	s = strings.TrimSuffix(s, "_KEY")
	return "provider:" + strings.ToLower(s)
}

func envKey(name string) string {
	if keyLookup != nil {
		if v, ok := keyLookup(settingKey(name)); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return strings.TrimSpace(os.Getenv(name))
}
