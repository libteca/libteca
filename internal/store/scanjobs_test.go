package store_test

import (
	"path/filepath"
	"testing"

	"github.com/libteca/libteca/internal/store"
)

func openTestDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestScanJobLifecycle(t *testing.T) {
	db := openTestDB(t)
	libID, err := db.AddLibrary("books", "audiobooks", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	jobID, err := db.CreateScanJob(libID)
	if err != nil {
		t.Fatal(err)
	}
	j, err := db.GetScanJob(jobID)
	if err != nil {
		t.Fatal(err)
	}
	if j.Status != "running" || j.FinishedAt != nil || j.Error != nil {
		t.Fatalf("new job = %+v, want running with no finish/error", j)
	}
	if j.StartedAt == 0 || j.CreatedAt == 0 {
		t.Fatalf("new job missing timestamps: %+v", j)
	}

	if err := db.UpdateScanJobCounts(jobID, 10, 8, 3, 5, 2); err != nil {
		t.Fatal(err)
	}
	j, err = db.GetScanJob(jobID)
	if err != nil {
		t.Fatal(err)
	}
	if j.FilesSeen != 10 || j.FilesProbed != 8 || j.FilesAdded != 3 || j.FilesUpdated != 5 || j.WorksChanged != 2 {
		t.Fatalf("counts not persisted: %+v", j)
	}

	msg := "boom"
	if err := db.FinishScanJob(jobID, "error", &msg); err != nil {
		t.Fatal(err)
	}
	j, err = db.GetScanJob(jobID)
	if err != nil {
		t.Fatal(err)
	}
	if j.Status != "error" || j.Error == nil || *j.Error != "boom" || j.FinishedAt == nil {
		t.Fatalf("finished job = %+v", j)
	}

	job2, err := db.CreateScanJob(libID)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.FinishScanJob(job2, "done", nil); err != nil {
		t.Fatal(err)
	}

	jobs, err := db.ListScanJobs(libID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 2 || jobs[0].ID != job2 || jobs[1].ID != jobID {
		t.Fatalf("ListScanJobs order = %+v, want newest first", jobs)
	}

	if _, err := db.GetScanJob(9999); err != store.ErrNotFound {
		t.Fatalf("missing job err = %v, want ErrNotFound", err)
	}
}

func TestFailRunningScanJobs(t *testing.T) {
	db := openTestDB(t)
	libID, err := db.AddLibrary("books", "audiobooks", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	running, err := db.CreateScanJob(libID)
	if err != nil {
		t.Fatal(err)
	}
	done, err := db.CreateScanJob(libID)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.FinishScanJob(done, "done", nil); err != nil {
		t.Fatal(err)
	}

	n, err := db.FailRunningScanJobs()
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("affected = %d, want 1", n)
	}
	j, err := db.GetScanJob(running)
	if err != nil {
		t.Fatal(err)
	}
	if j.Status != "error" || j.Error == nil {
		t.Fatalf("stale job = %+v, want error/interrupted", j)
	}
	j, err = db.GetScanJob(done)
	if err != nil {
		t.Fatal(err)
	}
	if j.Status != "done" {
		t.Fatalf("done job clobbered: %+v", j)
	}
}
