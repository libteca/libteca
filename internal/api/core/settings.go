package core

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
)

type providerKeyDef struct {
	Name, Label, Env, Setting string
}

var providerKeyDefs = []providerKeyDef{
	{Name: "tmdb", Label: "TMDB", Env: "LIBTECA_TMDB_KEY", Setting: "provider:tmdb"},
	{Name: "comicvine", Label: "ComicVine", Env: "LIBTECA_COMICVINE_KEY", Setting: "provider:comicvine"},
	{Name: "audible", Label: "Audible", Env: "", Setting: ""},
	{Name: "musicbrainz", Label: "MusicBrainz", Env: "", Setting: ""},
	{Name: "openlibrary", Label: "OpenLibrary", Env: "", Setting: ""},
}

func maskKey(v string) string {
	if len(v) <= 4 {
		return "••••"
	}
	return "••••" + v[len(v)-4:]
}

func (a *API) providerKeysPayload() []map[string]any {
	out := []map[string]any{}
	for _, d := range providerKeyDefs {
		m := map[string]any{"name": d.Name, "label": d.Label, "keyed": d.Env != ""}
		if d.Env == "" {
			m["configured"] = true
			m["masked"] = ""
			m["fromEnv"] = false
		} else {
			v, _ := a.DB.GetSetting(d.Setting)
			fromEnv := false
			if v == "" {
				if ev := strings.TrimSpace(os.Getenv(d.Env)); ev != "" {
					v = ev
					fromEnv = true
				}
			}
			m["configured"] = v != ""
			m["masked"] = ""
			if v != "" {
				m["masked"] = maskKey(v)
			}
			m["fromEnv"] = fromEnv
		}
		out = append(out, m)
	}
	return out
}

func (a *API) providerKeysGet(w http.ResponseWriter, r *http.Request) {
	if !a.requireAdmin(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"providers": a.providerKeysPayload()})
}

func (a *API) providerKeysPut(w http.ResponseWriter, r *http.Request) {
	if !a.requireAdmin(w, r) {
		return
	}
	var body struct {
		Keys map[string]string `json:"keys"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "bad json"})
		return
	}
	byName := map[string]providerKeyDef{}
	for _, d := range providerKeyDefs {
		byName[d.Name] = d
	}
	// Validate the whole key set before any write: map iteration order
	// used to commit the first valid key and then 400 on a later invalid
	// one, hiding a partial update behind an error response.
	for name := range body.Keys {
		d, ok := byName[name]
		if !ok {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "unknown provider: " + name})
			return
		}
		if d.Env == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": d.Label + " needs no key"})
			return
		}
	}
	changed := 0
	for name, val := range body.Keys {
		d := byName[name]
		val = strings.TrimSpace(val)
		var err error
		if val == "" {
			err = a.DB.DeleteSetting(d.Setting)
		} else {
			err = a.DB.SetSetting(d.Setting, val)
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "failed to save key"})
			return
		}
		changed++
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "changed": changed, "providers": a.providerKeysPayload()})
}
