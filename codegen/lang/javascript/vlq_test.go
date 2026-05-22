package javascript

import "testing"

func TestEncodeVLQ(t *testing.T) {
	cases := []struct {
		in   int
		want string
	}{
		{0, "A"},
		{1, "C"},
		{-1, "D"},
		{2, "E"},
		{-2, "F"},
		{16, "gB"},
		{-16, "hB"},
		{123, "2H"},
		{-123, "3H"},
		{456, "wc"},
	}
	for _, c := range cases {
		if got := encodeVLQ(c.in); got != c.want {
			t.Errorf("encodeVLQ(%d) = %q want %q", c.in, got, c.want)
		}
	}
}
