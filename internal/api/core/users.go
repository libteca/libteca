package core

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/libteca/libteca/internal/api/opds"
	"github.com/libteca/libteca/internal/auth"
	"github.com/libteca/libteca/internal/store"
	"github.com/neutron-build/neutron/go/neutron"
)

const minPasswordLen = 8

// MountUsers registers the user and token management endpoints. Add this one
// line to API.Mount in core.go:
//
//	a.MountUsers(r)
func (a *API) MountUsers(r *neutron.Router) {
	r.HandleFunc("GET /users", a.usersList)
	r.HandleFunc("POST /users", a.userCreate)
	r.HandleFunc("DELETE /users/{id}", a.userDelete)
	r.HandleFunc("POST /users/{id}/password", a.userSetPassword)
	r.HandleFunc("GET /tokens", a.tokensList)
	r.HandleFunc("POST /tokens", a.tokenIssue)
	r.HandleFunc("DELETE /tokens/{id}", a.tokenRevoke)
}

func (a *API) currentUser(r *http.Request) (*store.User, bool) {
	u, err := a.DB.User(auth.UserID(r))
	if err != nil {
		return nil, false
	}
	return u, true
}

func (a *API) requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	u, ok := a.currentUser(r)
	if !ok || !u.IsAdmin {
		writeJSON(w, 403, map[string]string{"error": "admin required"})
		return false
	}
	return true
}

func (a *API) isAdminRequest(r *http.Request) bool {
	u, ok := a.currentUser(r)
	return ok && u.IsAdmin
}

func userJSON(u *store.User) map[string]any {
	return map[string]any{"id": u.ID, "name": u.Name, "isAdmin": u.IsAdmin, "createdAtMs": u.CreatedAt}
}

func tokenJSON(t *store.Token) map[string]any {
	return map[string]any{
		"id": t.ID, "userId": t.UserID, "label": t.Label,
		"createdAtMs": t.CreatedAt, "lastSeenAtMs": t.LastSeenAt, "revokedAtMs": t.RevokedAt,
	}
}

func (a *API) usersList(w http.ResponseWriter, r *http.Request) {
	if !a.requireAdmin(w, r) {
		return
	}
	users, err := a.DB.Users()
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "internal error"})
		return
	}
	out := make([]map[string]any, 0, len(users))
	for i := range users {
		out = append(out, userJSON(&users[i]))
	}
	writeJSON(w, 200, out)
}

func (a *API) userCreate(w http.ResponseWriter, r *http.Request) {
	if !a.requireAdmin(w, r) {
		return
	}
	var body struct {
		Name     string `json:"name"`
		Password string `json:"password"`
		IsAdmin  bool   `json:"isAdmin"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
		writeJSON(w, 400, map[string]string{"error": "name required"})
		return
	}
	if len(body.Password) < minPasswordLen {
		writeJSON(w, 400, map[string]string{"error": "password must be at least 8 characters"})
		return
	}
	if _, err := a.DB.UserByName(body.Name); err == nil {
		writeJSON(w, 409, map[string]string{"error": "user exists"})
		return
	}
	id, err := a.DB.CreateUser(body.Name, auth.Hash(body.Password), body.IsAdmin)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, 201, map[string]any{"id": id})
}

func (a *API) userDelete(w http.ResponseWriter, r *http.Request) {
	if !a.requireAdmin(w, r) {
		return
	}
	id := auth.Atoi64(r.PathValue("id"))
	if id == auth.UserID(r) {
		writeJSON(w, 400, map[string]string{"error": "cannot delete self"})
		return
	}
	values, err := a.DB.DeleteUserGuarded(id)
	if errors.Is(err, store.ErrLastAdmin) {
		writeJSON(w, 400, map[string]string{"error": "cannot delete last admin"})
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, 404, map[string]string{"error": "user not found"})
		return
	}
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "internal error"})
		return
	}
	for _, v := range values {
		auth.InvalidateToken(v)
	}
	opds.InvalidateUser(id)
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (a *API) userSetPassword(w http.ResponseWriter, r *http.Request) {
	cur, ok := a.currentUser(r)
	if !ok {
		writeJSON(w, 401, map[string]string{"error": "unauthorized"})
		return
	}
	id := auth.Atoi64(r.PathValue("id"))
	if !cur.IsAdmin && cur.ID != id {
		writeJSON(w, 403, map[string]string{"error": "admin required"})
		return
	}
	var body struct {
		Password    string `json:"password"`
		OldPassword string `json:"oldPassword"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Password) < minPasswordLen {
		writeJSON(w, 400, map[string]string{"error": "password must be at least 8 characters"})
		return
	}
	if !cur.IsAdmin && !auth.Verify(body.OldPassword, cur.PasswordHash) {
		writeJSON(w, 403, map[string]string{"error": "wrong password"})
		return
	}
	if _, err := a.DB.User(id); err != nil {
		writeJSON(w, 404, map[string]string{"error": "user not found"})
		return
	}
	if err := a.DB.UpdateUserPassword(id, auth.Hash(body.Password)); err != nil {
		writeJSON(w, 500, map[string]string{"error": "internal error"})
		return
	}
	opds.InvalidateUser(id)
	values, err := a.DB.UserTokenValues(id)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "internal error"})
		return
	}
	if err := a.DB.RevokeUserTokens(id); err != nil {
		writeJSON(w, 500, map[string]string{"error": "internal error"})
		return
	}
	for _, v := range values {
		auth.InvalidateToken(v)
	}
	if err := a.DB.DeleteSetting("subsonic.pw." + strconv.FormatInt(id, 10)); err != nil {
		writeJSON(w, 500, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (a *API) tokensList(w http.ResponseWriter, r *http.Request) {
	cur, ok := a.currentUser(r)
	if !ok {
		writeJSON(w, 401, map[string]string{"error": "unauthorized"})
		return
	}
	uid := cur.ID
	if cur.IsAdmin {
		uid = 0
	}
	toks, err := a.DB.Tokens(uid)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "internal error"})
		return
	}
	out := make([]map[string]any, 0, len(toks))
	for i := range toks {
		out = append(out, tokenJSON(&toks[i]))
	}
	writeJSON(w, 200, out)
}

func (a *API) tokenIssue(w http.ResponseWriter, r *http.Request) {
	cur, ok := a.currentUser(r)
	if !ok {
		writeJSON(w, 401, map[string]string{"error": "unauthorized"})
		return
	}
	var body struct {
		Label string `json:"label"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, 400, map[string]string{"error": "bad request"})
		return
	}
	label := body.Label
	if label == "" {
		label = r.UserAgent()
	}
	value, err := auth.IssueToken(a.DB, cur.ID, label)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "token issue failed"})
		return
	}
	writeJSON(w, 201, map[string]any{"token": value})
}

func (a *API) tokenRevoke(w http.ResponseWriter, r *http.Request) {
	cur, ok := a.currentUser(r)
	if !ok {
		writeJSON(w, 401, map[string]string{"error": "unauthorized"})
		return
	}
	id := auth.Atoi64(r.PathValue("id"))
	t, err := a.DB.TokenByID(id)
	if err != nil {
		writeJSON(w, 404, map[string]string{"error": "token not found"})
		return
	}
	if !cur.IsAdmin && t.UserID != cur.ID {
		writeJSON(w, 403, map[string]string{"error": "admin required"})
		return
	}
	value, err := a.DB.RevokeToken(id)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "internal error"})
		return
	}
	auth.InvalidateToken(value)
	writeJSON(w, 200, map[string]any{"ok": true})
}
