package core

import (
	"path/filepath"
	"testing"
)

func TestImportRequiresAdmin(t *testing.T) {
	env := newLinkingEnv(t)
	code, body := env.do(t, "POST", "/import/abs", env.userToken, `{"dataDir":"/tmp"}`)
	if code != 403 {
		t.Fatalf("non-admin abs import = %d %v, want 403", code, body)
	}
	code, body = env.do(t, "POST", "/import/kavita", env.userToken, `{"dbPath":"/tmp/app.db"}`)
	if code != 403 {
		t.Fatalf("non-admin kavita import = %d %v, want 403", code, body)
	}
}

func TestImportValidation(t *testing.T) {
	env := newLinkingEnv(t)
	code, body := env.do(t, "POST", "/import/abs", env.adminToken, `{}`)
	if code != 400 {
		t.Fatalf("abs without dataDir = %d %v", code, body)
	}
	code, _ = env.do(t, "POST", "/import/abs", env.adminToken, `{"dataDir":"/nonexistent-dir-xyz"}`)
	if code != 400 {
		t.Fatalf("abs with missing dir = %d, want 400", code)
	}
	empty := t.TempDir()
	code, _ = env.do(t, "POST", "/import/abs", env.adminToken, `{"dataDir":"`+empty+`"}`)
	if code != 400 {
		t.Fatalf("abs with dir but no abs_database.db = %d, want 400", code)
	}
	code, _ = env.do(t, "POST", "/import/kavita", env.adminToken, `{}`)
	if code != 400 {
		t.Fatalf("kavita without dbPath = %d, want 400", code)
	}
	code, _ = env.do(t, "POST", "/import/kavita", env.adminToken, `{"dbPath":"`+filepath.Join(empty, "app.db")+`"}`)
	if code != 400 {
		t.Fatalf("kavita with missing file = %d, want 400", code)
	}
}
