package importer

import (
	"errors"
	"strings"
	"testing"
)

func TestImportTempPasswordEntropyFailureRollsBack(t *testing.T) {
	f := newAtomicImportFixture(t, "abs")
	db := openStore(t)
	before := importSnapshot(t, db.DB, atomicImportTables)

	orig := readRandom
	readRandom = func([]byte) (int, error) { return 0, errors.New("entropy exhausted") }
	plan, err := f.run(db, false)
	readRandom = orig

	if err == nil || !strings.Contains(err.Error(), "temporary password") {
		t.Fatalf("entropy failure during import: plan=%v err=%v, want temporary-password error", plan, err)
	}
	if plan != nil {
		t.Fatal("failed import returned a successful plan")
	}
	assertImportSnapshot(t, before, importSnapshot(t, db.DB, atomicImportTables))

	if _, err := f.run(db, false); err != nil {
		t.Fatalf("retry after restoring randomness: %v", err)
	}
	var users int
	if err := db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&users); err != nil {
		t.Fatal(err)
	}
	if users == 0 {
		t.Fatal("retry after entropy failure must create the imported users")
	}
}
