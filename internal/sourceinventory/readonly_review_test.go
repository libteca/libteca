package sourceinventory

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestReadonlyReviewRetainsOrphanIdentityAndCounts(t *testing.T) {
	path, _ := snapshotFixture(t)
	snapshotURL := url.URL{Scheme: "file", Path: filepath.ToSlash(path)}
	db, err := sql.Open("sqlite", snapshotURL.String())
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	for _, statement := range []string{
		`PRAGMA foreign_keys=OFF`,
		`UPDATE editions SET work_id=999 WHERE id=70`,
		`UPDATE works SET library_id=999 WHERE id=6`,
		`UPDATE files SET edition_id=999 WHERE id=701`,
		`UPDATE progress SET edition_id=999, file_id=701, revision=876, deleted=1 WHERE id=3`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	before := treeState(t, filepath.Dir(path))
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	report, err := Read(context.Background(), path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.SnapshotSHA256 != fmt.Sprintf("%x", sha256.Sum256(content)) {
		t.Fatal("report fingerprint does not identify the input snapshot")
	}
	if !hasFinding(editionByID(t, report, 70).Findings, "missing_work") || !hasFinding(editionByID(t, report, 60).Findings, "missing_library") {
		t.Fatal("orphan parent relationship omitted")
	}
	if len(report.UnattachedFiles) != 1 || report.UnattachedFiles[0].ID != 701 || report.UnattachedFiles[0].EditionID == nil || *report.UnattachedFiles[0].EditionID != 999 {
		t.Fatal("orphan file reference was lost")
	}
	if len(report.OrphanProgress) != 1 {
		t.Fatalf("orphan progress omitted: %+v", report.OrphanProgress)
	}
	p := report.OrphanProgress[0]
	if p.ID != 3 || p.UserID != 1 || p.EditionID != 999 || p.FileID == nil || *p.FileID != 701 || p.Revision != 876 || !p.Deleted || p.UpdatedAt != 1236 {
		t.Fatalf("orphan progress identity or revision changed: %+v", p)
	}
	counts := map[string]int{}
	count := func(findings []string) {
		for _, finding := range findings {
			counts[finding]++
		}
	}
	for _, library := range report.Libraries {
		count(library.Findings)
	}
	for _, edition := range report.Editions {
		count(edition.Findings)
		for _, file := range edition.Files {
			count(file.Findings)
		}
		for _, progress := range edition.Progress {
			count(progress.Findings)
		}
	}
	for _, file := range report.UnattachedFiles {
		count(file.Findings)
	}
	for _, progress := range report.OrphanProgress {
		count(progress.Findings)
	}
	if !reflect.DeepEqual(report.FindingCounts, counts) {
		t.Fatalf("summary omits emitted findings: got=%v want=%v", report.FindingCounts, counts)
	}
	if after := treeState(t, filepath.Dir(path)); !reflect.DeepEqual(before, after) {
		t.Fatal("inventory changed the inconsistent input fixture")
	}
}
