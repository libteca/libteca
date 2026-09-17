package natural

import "strings"

func digit(c byte) bool { return c >= '0' && c <= '9' }

// Less compares strings in natural order: digit runs compare numerically
// after stripping leading zeros, everything else bytewise. It is the single
// ordering contract shared by the scanner, OPDS CBZ listing and importer so
// page numbers, file sequences and progress mapping agree across faces.
func Less(a, b string) bool {
	ai, bi := 0, 0
	for ai < len(a) && bi < len(b) {
		ca, cb := a[ai], b[bi]
		if digit(ca) && digit(cb) {
			na, nj := ai, bi
			for na < len(a) && digit(a[na]) {
				na++
			}
			for nj < len(b) && digit(b[nj]) {
				nj++
			}
			da, db := strings.TrimLeft(a[ai:na], "0"), strings.TrimLeft(b[bi:nj], "0")
			if len(da) != len(db) {
				return len(da) < len(db)
			}
			if da != db {
				return da < db
			}
			ai, bi = na, nj
			continue
		}
		if ca != cb {
			return ca < cb
		}
		ai++
		bi++
	}
	return len(a)-ai < len(b)-bi
}
