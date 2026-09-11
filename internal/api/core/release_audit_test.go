package core

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/libteca/libteca/internal/auth"
	"github.com/libteca/libteca/internal/scan"
	"github.com/libteca/libteca/internal/store"
	"github.com/libteca/libteca/internal/transcode"
)

func TestCoverRejectsTraversalNames(t *testing.T) {
	a, _, _, _ := newTestAPI(t, nil)
	for _, name := range []string{"..", ".", "a/b", `..\..\etc`, "..."} {
		req := httptest.NewRequest("GET", "/covers/"+name, nil)
		req.SetPathValue("cover", name)
		rec := httptest.NewRecorder()
		a.cover(rec, req)
		if rec.Code != 404 {
			t.Fatalf("cover %q = %d %s, want 404", name, rec.Code, rec.Body.String())
		}
	}
}

func TestScanJobErrorMaskedForNonAdmin(t *testing.T) {
	a, db, libA, _ := newTestAPI(t, func(ctx context.Context, db *store.DB, lib *store.Library, coversDir string, onProgress scan.ProgressFn) (int, error) {
		return 0, fmt.Errorf("open /srv/media: permission denied")
	})
	res, err := db.Exec(`INSERT INTO users (name, password_hash, is_admin, created_at, updated_at) VALUES ('plain','x',0,0,0)`)
	if err != nil {
		t.Fatal(err)
	}
	plainUID, _ := res.LastInsertId()

	if code, _ := postScan(t, a, libA); code != 202 {
		t.Fatalf("scan start = %d, want 202", code)
	}
	j := waitJobDone(t, db, libA)
	if j.Status != "error" {
		t.Fatalf("job = %+v, want error", j)
	}

	getJob := func(uid int64) map[string]any {
		t.Helper()
		req := httptest.NewRequest("GET", "/scan-jobs/"+strconv.FormatInt(j.ID, 10), nil)
		req.SetPathValue("id", strconv.FormatInt(j.ID, 10))
		req = auth.WithUser(req, uid)
		rec := httptest.NewRecorder()
		a.scanJob(rec, req)
		var out map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("bad body %q: %v", rec.Body.String(), err)
		}
		return out
	}

	adminView := getJob(testAdminUID)
	if adminView["error"] != "open /srv/media: permission denied" {
		t.Fatalf("admin error = %v, want detail", adminView["error"])
	}
	plainView := getJob(plainUID)
	if plainView["error"] == nil || plainView["error"].(string) != "error" {
		t.Fatalf("non-admin error = %v, want generic", plainView["error"])
	}
	if strings.Contains(fmt.Sprint(plainView), "/srv/media") {
		t.Fatalf("non-admin view leaks path: %v", plainView)
	}

	req := httptest.NewRequest("GET", "/libraries/"+strconv.FormatInt(libA, 10)+"/scan/jobs", nil)
	req.SetPathValue("id", strconv.FormatInt(libA, 10))
	req = auth.WithUser(req, plainUID)
	rec := httptest.NewRecorder()
	a.scanJobs(rec, req)
	if strings.Contains(rec.Body.String(), "/srv/media") {
		t.Fatalf("jobs list leaks path: %s", rec.Body.String())
	}
}

type maskedSSEEvent struct {
	JobID       int64  `json:"jobId"`
	Status      string `json:"status"`
	Error       string `json:"error"`
	CurrentPath string `json:"currentPath"`
}

func TestScanEventsMaskPathsForNonAdmin(t *testing.T) {
	started := make(chan int64, 4)
	release := make(chan struct{})
	a, _, libA, _ := newTestAPI(t, blockingScan(started, release))
	srv := mountTestServer(t, a)

	if code, _ := postScan(t, a, libA); code != 202 {
		t.Fatalf("scan start = %d, want 202", code)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("stub never started")
	}

	resp, err := srv.Client().Get(srv.URL + "/api/core/libraries/" + strconv.FormatInt(libA, 10) + "/scan/events")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type = %q", ct)
	}

	done := make(chan string, 32)
	go func() {
		defer close(done)
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		var data string
		for sc.Scan() {
			line := sc.Text()
			if strings.HasPrefix(line, "event:") || strings.HasPrefix(line, ":") || strings.HasPrefix(line, "retry:") {
				continue
			}
			if strings.HasPrefix(line, "data:") {
				data = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
				continue
			}
			if line == "" && data != "" {
				select {
				case done <- data:
				default:
				}
				data = ""
			}
		}
	}()

	time.Sleep(300 * time.Millisecond)
	close(release)

	deadline := time.After(5 * time.Second)
	sawTerminal := false
	for !sawTerminal {
		select {
		case data, ok := <-done:
			if !ok {
				t.Fatal("stream closed before terminal event")
			}
			var ev maskedSSEEvent
			if err := json.Unmarshal([]byte(data), &ev); err != nil {
				t.Fatalf("bad sse data %q: %v", data, err)
			}
			if ev.CurrentPath != "" {
				t.Fatalf("non-admin saw currentPath %q in %+v", ev.CurrentPath, ev)
			}
			if ev.Status != "running" {
				sawTerminal = true
			}
		case <-deadline:
			t.Fatal("no terminal event")
		}
	}
}

func TestHLSFileEditionWithoutFiles(t *testing.T) {
	a, db, libA, _ := newTestAPI(t, nil)
	a.TC = transcode.New(a.DataDir)
	w := seedWork(t, db, libA, "NoFiles", nil, nil, 1, 1)
	e := seedEdition(t, db, w, nil)

	req := httptest.NewRequest("GET", "/hls/web-E/index.m3u8", nil)
	req.SetPathValue("sid", "web-"+strconv.FormatInt(e, 10))
	req.SetPathValue("file", "index.m3u8")
	rec := httptest.NewRecorder()
	a.hlsFile(rec, req)
	if rec.Code != 404 {
		t.Fatalf("hls file on empty edition = %d %s, want 404", rec.Code, rec.Body.String())
	}
}
