package html

import "testing"

func TestPathFromHref(t *testing.T) {
	tests := []struct {
		href, want string
	}{
		{"/", "index.html"},
		{"", "index.html"},
		{"/index.html", "index.html"},
		{"/about.html", "about.html"},
		{"/docs/sngl/components/avatar.html", "docs/sngl/components/avatar.html"},
		{"/foo/", "foo/index.html"},
		{"/foo", "foo/index.html"},
		{"/foo/bar/", "foo/bar/index.html"},
	}
	for _, tc := range tests {
		got := pathFromHref(tc.href)
		if got != tc.want {
			t.Errorf("pathFromHref(%q) = %q, want %q", tc.href, got, tc.want)
		}
	}
}
