package core

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAddLibraryRejectsOverlappingRoots(t *testing.T) {
	_, base, token := newUsersEnv(t)
	root := t.TempDir()
	inner := filepath.Join(root, "inner")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	code, _ := callJSON(t, "POST", base+"/libraries", token, map[string]any{"name": "a", "type": "audiobooks", "path": root})
	if code != 201 {
		t.Fatalf("create root library = %d", code)
	}
	cases := []struct {
		path string
		desc string
	}{
		{root, "identical path"},
		{inner, "descendant"},
		{filepath.Dir(root), "ancestor"},
	}
	for _, c := range cases {
		code, body := callJSON(t, "POST", base+"/libraries", token, map[string]any{"name": "b", "type": "books", "path": c.path})
		if code != 409 {
			t.Fatalf("%s = %d (%v), want 409", c.desc, code, body)
		}
	}
	sibling := t.TempDir()
	code, _ = callJSON(t, "POST", base+"/libraries", token, map[string]any{"name": "c", "type": "books", "path": sibling})
	if code != 201 {
		t.Fatalf("sibling root rejected: %d", code)
	}
}
