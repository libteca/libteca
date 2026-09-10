package corpusutil

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

type FixtureRequest struct {
	Method  string              `json:"method"`
	Path    string              `json:"path"`
	Query   map[string][]string `json:"query"`
	Headers map[string]string   `json:"headers"`
	Body    any                 `json:"body"`
	BodyB64 string              `json:"body_b64"`
}

type FixtureResponse struct {
	Status      int    `json:"status"`
	ContentType string `json:"content_type"`
	Body        any    `json:"body"`
	BodyB64     string `json:"body_b64"`
	Truncated   bool   `json:"truncated"`
}

type Fixture struct {
	Face     string          `json:"face"`
	Flow     string          `json:"flow"`
	Seq      int             `json:"seq"`
	File     string          `json:"-"`
	Request  FixtureRequest  `json:"request"`
	Response FixtureResponse `json:"response"`
}

func Load(dir string) ([]Fixture, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	var out []Fixture
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		var fx Fixture
		if err := json.Unmarshal(data, &fx); err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		fx.File = filepath.Base(f)
		out = append(out, fx)
	}
	return out, nil
}

var varRe = regexp.MustCompile(`\{[a-zA-Z][a-zA-Z0-9]*\}`)
var userRe = regexp.MustCompile(`^user_\d+$`)

func IsWildcard(s string) bool {
	if s == "[REDACTED]" || s == "{uuid}" || s == "{token}" {
		return true
	}
	return userRe.MatchString(s)
}

func ApplyVars(v any, vars map[string]string) any {
	switch x := v.(type) {
	case string:
		s := x
		for k, val := range vars {
			s = strings.ReplaceAll(s, k, val)
		}
		return s
	case map[string]any:
		for k, val := range x {
			x[k] = ApplyVars(val, vars)
		}
		return x
	case []any:
		for i := range x {
			x[i] = ApplyVars(x[i], vars)
		}
		return x
	}
	return v
}

func Unresolved(v any) []string {
	seen := map[string]bool{}
	var walk func(any)
	walk = func(x any) {
		switch t := x.(type) {
		case string:
			for _, m := range varRe.FindAllString(t, -1) {
				if m != "{uuid}" && m != "{token}" {
					seen[m] = true
				}
			}
		case map[string]any:
			for _, val := range t {
				walk(val)
			}
		case []any:
			for _, val := range t {
				walk(val)
			}
		}
	}
	walk(v)
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func Harvest(pathSuffix, idKey, reqPath string, body any, vars map[string]string) {
	if pathSuffix == "" || !strings.HasSuffix(strings.ToLower(reqPath), pathSuffix) {
		return
	}
	m, ok := body.(map[string]any)
	if !ok {
		return
	}
	if s, ok := m[idKey].(string); ok && s != "" {
		vars["{sessionId}"] = s
	}
}

var ignoreKeys = map[string]bool{
	"ServerId": true, "ServerName": true, "sessionId": true, "SessionId": true,
	"PlaySessionId": true, "userToken": true, "AccessToken": true, "token": true,
	"Token": true, "serverTime": true, "lastUpdate": true, "startTime": true,
	"Path": true, "TranscodingUrl": true, "Etag": true, "DateCreated": true,
}

func ignoredKey(k string) bool {
	if ignoreKeys[k] {
		return true
	}
	return strings.HasSuffix(k, "_at") || strings.HasSuffix(k, "At")
}

// Match asserts expected is a subset of actual: maps by key (missing keys are
// diffs), arrays index-wise with len(actual) >= len(expected), scalars equal.
// Ignored keys and wildcard values always pass.
func Match(path string, expected, actual any) []string {
	var diffs []string
	var walk func(string, any, any)
	walk = func(p string, exp, act any) {
		switch e := exp.(type) {
		case map[string]any:
			am, ok := act.(map[string]any)
			if !ok {
				diffs = append(diffs, fmt.Sprintf("%s: expected object, got %T", p, act))
				return
			}
			keys := make([]string, 0, len(e))
			for k := range e {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				if ignoredKey(k) {
					continue
				}
				av, ok := am[k]
				if !ok {
					diffs = append(diffs, fmt.Sprintf("%s.%s: missing in actual", p, k))
					continue
				}
				walk(p+"."+k, e[k], av)
			}
		case []any:
			aa, ok := act.([]any)
			if !ok {
				diffs = append(diffs, fmt.Sprintf("%s: expected array, got %T", p, act))
				return
			}
			if len(aa) < len(e) {
				diffs = append(diffs, fmt.Sprintf("%s: expected at least %d elements, got %d", p, len(e), len(aa)))
				return
			}
			for i := range e {
				walk(fmt.Sprintf("%s.%d", p, i), e[i], aa[i])
			}
		case string:
			if IsWildcard(e) {
				return
			}
			if s, ok := act.(string); ok && s == e {
				return
			}
			diffs = append(diffs, fmt.Sprintf("%s: expected %q, got %#v", p, e, act))
		case float64:
			if f, ok := act.(float64); ok && f == e {
				return
			}
			diffs = append(diffs, fmt.Sprintf("%s: expected %v, got %#v", p, e, act))
		case bool:
			if b, ok := act.(bool); ok && b == e {
				return
			}
			diffs = append(diffs, fmt.Sprintf("%s: expected %v, got %#v", p, e, act))
		case nil:
			if act == nil {
				return
			}
			diffs = append(diffs, fmt.Sprintf("%s: expected null, got %#v", p, act))
		default:
			if !reflect.DeepEqual(exp, act) {
				diffs = append(diffs, fmt.Sprintf("%s: expected %#v, got %#v", p, exp, act))
			}
		}
	}
	walk(path, expected, actual)
	return diffs
}

func RequestBody(r *FixtureRequest, vars map[string]string, username, password string) ([]byte, error) {
	if r.Body == nil && r.BodyB64 == "" {
		return nil, nil
	}
	if r.Body == nil {
		return base64.StdEncoding.DecodeString(r.BodyB64)
	}
	applied := ApplyVars(r.Body, vars)
	if m, ok := applied.(map[string]any); ok {
		for k, repl := range map[string]string{
			"username": username, "Username": username,
			"password": password, "Password": password, "Pw": password,
		} {
			if _, exists := m[k]; exists {
				m[k] = repl
			}
		}
	}
	return json.Marshal(applied)
}

type ReplayOpts struct {
	AuthHeader    string
	TokenHeader   string
	Username      string
	Password      string
	HarvestSuffix string
	HarvestKey    string
}

func Replay(t *testing.T, client *http.Client, base string, fx Fixture, vars map[string]string, o ReplayOpts) {
	t.Helper()
	path := ApplyVars(fx.Request.Path, vars).(string)
	q := url.Values{}
	for k, vals := range fx.Request.Query {
		for _, v := range vals {
			sv := ApplyVars(v, vars).(string)
			if sv == "[REDACTED]" {
				switch strings.ToLower(k) {
				case "api_key", "apikey", "token":
					sv = o.TokenHeader
				}
			}
			q.Add(k, sv)
		}
	}
	body, err := RequestBody(&fx.Request, vars, o.Username, o.Password)
	if err != nil {
		t.Fatal(err)
	}
	unresolved := Unresolved(path)
	unresolved = append(unresolved, Unresolved(q.Encode())...)
	unresolved = append(unresolved, Unresolved(string(body))...)
	if len(unresolved) > 0 {
		t.Skipf("unresolved placeholders %v", unresolved)
	}
	target := base + path
	if enc := q.Encode(); enc != "" {
		target += "?" + enc
	}
	var rd io.Reader
	if len(body) > 0 {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequest(fx.Request.Method, target, rd)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range fx.Request.Headers {
		switch strings.ToLower(k) {
		case "host", "content-length", "connection", "accept-encoding", "transfer-encoding":
			continue
		case "authorization":
			if v == "[REDACTED]" {
				v = o.AuthHeader
			} else {
				v = ApplyVars(v, vars).(string)
			}
		case "x-emby-token", "x-mediabrowser-token", "x-mediabrowser token":
			if v == "[REDACTED]" {
				v = o.TokenHeader
			}
		default:
			v = ApplyVars(v, vars).(string)
		}
		req.Header.Set(k, v)
	}
	if len(body) > 0 && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	act, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != fx.Response.Status {
		snippet := act
		if len(snippet) > 200 {
			snippet = snippet[:200]
		}
		t.Fatalf("status: expected %d, got %d (body: %s)", fx.Response.Status, resp.StatusCode, snippet)
	}
	if fx.Response.Body == nil {
		var actual any
		if json.Unmarshal(act, &actual) == nil {
			Harvest(o.HarvestSuffix, o.HarvestKey, fx.Request.Path, actual, vars)
		}
		return
	}
	var actual any
	if err := json.Unmarshal(act, &actual); err != nil {
		t.Fatalf("actual response is not JSON: %v", err)
	}
	Harvest(o.HarvestSuffix, o.HarvestKey, fx.Request.Path, actual, vars)
	if fx.Response.ContentType != "" {
		want, _, _ := mime.ParseMediaType(fx.Response.ContentType)
		got, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
		if want != got {
			t.Errorf("content-type: expected %q, got %q", want, got)
		}
	}
	expected := ApplyVars(fx.Response.Body, vars)
	diffs := Match("$", expected, actual)
	for i, d := range diffs {
		if i == 20 {
			t.Errorf("(%d more diffs suppressed)", len(diffs)-i)
			break
		}
		t.Errorf("subset mismatch: %s", d)
	}
}
