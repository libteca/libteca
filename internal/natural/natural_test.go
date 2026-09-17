package natural

import "testing"

func TestLess(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"2.mp3", "10.mp3", true},
		{"10.mp3", "2.mp3", false},
		{"02.mp3", "2.mp3", false},
		{"track2.mp3", "track10.mp3", true},
		{"CD1/01.mp3", "CD1/02.mp3", true},
		{"CD1/02.mp3", "CD2/01.mp3", true},
		{"CD1/x.mp3", "CD10/x.mp3", true},
		{"a2.jpg", "A10.jpg", false},
		{"A10.jpg", "a2.jpg", true},
		{"a.jpg", "a.jpg", false},
		{"abc", "abcd", true},
		{"0002.mp3", "00003.mp3", true},
	}
	for _, c := range cases {
		if got := Less(c.a, c.b); got != c.want {
			t.Errorf("Less(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
		if Less(c.a, c.b) && Less(c.b, c.a) {
			t.Errorf("Less not antisymmetric for %q/%q", c.a, c.b)
		}
	}
}
