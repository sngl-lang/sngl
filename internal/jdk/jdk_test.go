package jdk

import "testing"

func TestParseMajor(t *testing.T) {
	cases := []struct {
		in   string
		want int
		ok   bool
	}{
		{`openjdk version "21.0.12" 2026-07-21`, 21, true},
		{`openjdk version "25.0.2" 2026-01-20`, 25, true},
		{`openjdk version "17.0.9" 2023-10-17`, 17, true},
		{`java version "1.8.0_292"`, 8, true},
		{`nonsense`, 0, false},
	}
	for _, c := range cases {
		got, ok := parseMajor(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("parseMajor(%q) = %d,%v; want %d,%v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestPreferredInstall(t *testing.T) {
	cases := []struct {
		min, max, want int
	}{
		{17, 23, 21}, // 21 LTS fits
		{17, 20, 17}, // 21 too new → 17 LTS
		{22, 25, 25}, // no LTS in window → upper bound
		{17, 25, 21}, // widest → newest LTS
	}
	for _, c := range cases {
		if got := preferredInstall(c.min, c.max); got != c.want {
			t.Errorf("preferredInstall(%d,%d) = %d; want %d", c.min, c.max, got, c.want)
		}
	}
}
