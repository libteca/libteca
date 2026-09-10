package core

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"github.com/libteca/libteca/internal/importer"
	"github.com/neutron-build/neutron-go/neutron"
)

// MountImport registers the foreign-instance import endpoints (admin only).
// Add this one line to API.Mount in core.go:
//
//	a.MountImport(r)
func (a *API) MountImport(r *neutron.Router) {
	r.HandleFunc("POST /import/abs", a.importABS)
	r.HandleFunc("POST /import/kavita", a.importKavita)
}

// importABS: POST /api/core/import/abs {dataDir, dryRun} — dataDir is the
// Audiobookshelf config directory containing abs_database.db. Runs
// synchronously; dryRun returns the plan without writing anything.
func (a *API) importABS(w http.ResponseWriter, r *http.Request) {
	if !a.requireAdmin(w, r) {
		return
	}
	var body struct {
		DataDir string `json:"dataDir"`
		DryRun  bool   `json:"dryRun"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.DataDir == "" {
		writeJSON(w, 400, map[string]string{"error": "dataDir required"})
		return
	}
	fi, err := os.Stat(body.DataDir)
	if err != nil || !fi.IsDir() {
		writeJSON(w, 400, map[string]string{"error": "dataDir is not a directory"})
		return
	}
	plan, err := importer.ABS(body.DataDir, a.DB, body.DryRun)
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	logTempPasswords("abs", plan.Users, body.DryRun)
	writeJSON(w, 200, map[string]any{"dryRun": body.DryRun, "plan": plan})
}

// importKavita: POST /api/core/import/kavita {dbPath, dryRun} — dbPath is the
// Kavita app.db file. Runs synchronously; dryRun returns the plan only.
func (a *API) importKavita(w http.ResponseWriter, r *http.Request) {
	if !a.requireAdmin(w, r) {
		return
	}
	var body struct {
		DBPath string `json:"dbPath"`
		DryRun bool   `json:"dryRun"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.DBPath == "" {
		writeJSON(w, 400, map[string]string{"error": "dbPath required"})
		return
	}
	fi, err := os.Stat(body.DBPath)
	if err != nil || fi.IsDir() {
		writeJSON(w, 400, map[string]string{"error": "dbPath is not a file"})
		return
	}
	plan, err := importer.Kavita(body.DBPath, a.DB, body.DryRun)
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	logTempPasswords("kavita", plan.Users, body.DryRun)
	writeJSON(w, 200, map[string]any{"dryRun": body.DryRun, "plan": plan})
}

func logTempPasswords(source string, users []importer.UserPlan, dryRun bool) {
	if dryRun {
		return
	}
	for _, u := range users {
		if u.TempPassword != "" {
			fmt.Printf("libteca: import %s: user %q temp password %s (foreign hash cannot be migrated; admin resets)\n", source, u.Name, u.TempPassword)
		}
	}
}
