package middleware

import "testing"

func TestStatSkipsAssetsAndDashboardRequests(t *testing.T) {
	cases := map[string]bool{
		"/public/static/app.css": true,
		"/api/stat/req":          true,
		"/api/stat":              true,
		"/favicon.ico":           true,
		"/":                      false,
		"/explorer":              false,
		"/upload/a.txt":          false,
		"/api/status":            false,
	}
	for path, want := range cases {
		if got := statSkipped(path); got != want {
			t.Errorf("statSkipped(%q) = %v, want %v", path, got, want)
		}
	}
}
