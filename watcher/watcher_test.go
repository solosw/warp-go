package watcher

import "testing"

func TestShouldSkipOnlyVCSDepsAndAppStore(t *testing.T) {
	cases := []struct {
		path string
		skip bool
	}{
		{".env", false},
		{".github/workflows/ci.yml", false},
		{".vscode/settings.json", false},
		{".next/cache", false},
		{".git/config", true},
		{".svn/entries", true},
		{"node_modules/pkg/index.js", true},
		{"vendor/lib.go", true},
		{"__pycache__/x.pyc", true},
		{".warp-snapshots/x", true},
		{"src/main.go", false},
	}
	for _, tc := range cases {
		if got := shouldSkip(tc.path, ""); got != tc.skip {
			t.Errorf("shouldSkip(%q)=%v, want %v", tc.path, got, tc.skip)
		}
	}
}
