package meta

import "testing"

func TestMain(m *testing.M) {
	ResetCache()
	m.Run()
}
