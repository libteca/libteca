package core

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
)

func putKeys(t *testing.T, e *playlistsEnv, keys map[string]string) (int, map[string]any) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"keys": keys})
	req, _ := http.NewRequest(http.MethodPut, e.base+"/settings/providers", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.adminToken)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}

func getKeys(t *testing.T, e *playlistsEnv) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, e.base+"/settings/providers", nil)
	req.Header.Set("Authorization", "Bearer "+e.adminToken)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}

func findProvider(t *testing.T, out map[string]any, name string) map[string]any {
	t.Helper()
	for _, p := range out["providers"].([]any) {
		m := p.(map[string]any)
		if m["name"] == name {
			return m
		}
	}
	t.Fatalf("provider %s missing", name)
	return nil
}

func TestProviderKeysSetMaskClear(t *testing.T) {
	e := newPlaylistsEnv(t)

	code, out := getKeys(t, e)
	if code != http.StatusOK {
		t.Fatalf("get: %d", code)
	}
	tmdb := findProvider(t, out, "tmdb")
	if tmdb["configured"] != false {
		t.Fatalf("tmdb should start unconfigured: %v", tmdb)
	}
	ol := findProvider(t, out, "openlibrary")
	if ol["keyed"] != false || ol["configured"] != true {
		t.Fatalf("openlibrary should be keyless+configured: %v", ol)
	}

	code, out = putKeys(t, e, map[string]string{"tmdb": "abcd1234efgh5678"})
	if code != http.StatusOK {
		t.Fatalf("put: %d %v", code, out)
	}
	tmdb = findProvider(t, out, "tmdb")
	if tmdb["configured"] != true || tmdb["masked"] != "••••5678" {
		t.Fatalf("tmdb after set: %v", tmdb)
	}

	_, out = getKeys(t, e)
	tmdb = findProvider(t, out, "tmdb")
	if v, _ := e.db.GetSetting("provider:tmdb"); v != "abcd1234efgh5678" {
		t.Fatalf("stored value wrong: %q", v)
	}
	if tmdb["masked"] != "••••5678" {
		t.Fatalf("mask on re-read: %v", tmdb)
	}

	code, _ = putKeys(t, e, map[string]string{"nope": "x"})
	if code != http.StatusBadRequest {
		t.Fatalf("unknown provider: %d", code)
	}
	code, _ = putKeys(t, e, map[string]string{"audible": "x"})
	if code != http.StatusBadRequest {
		t.Fatalf("keyless provider: %d", code)
	}

	code, out = putKeys(t, e, map[string]string{"tmdb": ""})
	if code != http.StatusOK {
		t.Fatalf("clear: %d", code)
	}
	tmdb = findProvider(t, out, "tmdb")
	if tmdb["configured"] != false {
		t.Fatalf("tmdb after clear: %v", tmdb)
	}
	if _, ok := e.db.GetSetting("provider:tmdb"); ok {
		t.Fatal("setting should be deleted after clear")
	}
}
