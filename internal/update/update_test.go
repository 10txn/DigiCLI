package update

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewer(t *testing.T) {
	tests := []struct {
		current string
		latest  string
		want    bool
	}{
		// The ordinary cases.
		{"v0.1.2", "v0.1.3", true},
		{"v0.1.3", "v0.1.3", false},
		{"v0.1.3", "v0.1.2", false},
		{"v0.9.0", "v0.10.0", true}, // not a string comparison
		{"v0.1.2", "v1.0.0", true},

		// The tag is what the Makefile stamps, with or without the v.
		{"0.1.2", "v0.1.3", true},
		{"v0.1.2", "0.1.3", true},

		// A build with no release behind it is never told to upgrade: there
		// is nothing to compare, and "dev" is what a plain `go build` stamps.
		{"dev", "v0.1.3", false},
		{"", "v0.1.3", false},
		{"v0.1.2", "not-a-tag", false},

		// A dirty tree says nothing about which release the build came from.
		{"v0.1.2-dirty", "v0.1.3", true},
		{"v0.1.3-dirty", "v0.1.3", false},

		// git describe on a commit past a tag: that build is ahead of the
		// tag, so the release it is built past must not look newer.
		{"v0.1.3-4-gabc1234", "v0.1.3", false},
		{"v0.1.3-4-gabc1234", "v0.1.4", true},
		{"v0.1.3-4-gabc1234-dirty", "v0.1.3", false},

		// Prereleases sort below the release they lead to. /releases/latest
		// does not return them, but the running build can be one.
		{"v0.2.0-rc.1", "v0.2.0", true},
		{"v0.2.0", "v0.2.0-rc.1", false},
		{"v0.2.0-rc.1", "v0.2.0-rc.2", true},

		// Build metadata is not ordered.
		{"v0.1.2+build.7", "v0.1.2", false},
	}

	for _, tt := range tests {
		if got := Newer(tt.current, tt.latest); got != tt.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", tt.current, tt.latest, got, tt.want)
		}
	}
}

func TestComparable(t *testing.T) {
	for _, v := range []string{"v0.1.2", "0.1.2", "v1.0.0-rc.1", "v0.1.2-4-gabc1234"} {
		if !Comparable(v) {
			t.Errorf("Comparable(%q) = false, want true", v)
		}
	}
	for _, v := range []string{"dev", "", "v0.1", "main", "0.1.x"} {
		if Comparable(v) {
			t.Errorf("Comparable(%q) = true, want false", v)
		}
	}
}

// serve points Latest at a local server for the test's duration.
func serve(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	previous := apiBase
	apiBase = srv.URL
	t.Cleanup(func() { apiBase = previous })
}

func TestLatest(t *testing.T) {
	var gotPath, gotAgent string
	serve(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAgent = r.URL.Path, r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"tag_name": "v0.1.3",
			"html_url": "https://github.com/10txn/digicli/releases/tag/v0.1.3",
			"body": "notes"
		}`))
	})

	release, err := Latest(context.Background(), "v0.1.2")
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if release.Version != "v0.1.3" {
		t.Errorf("version: got %q, want v0.1.3", release.Version)
	}
	if release.URL == "" {
		t.Error("the release URL is empty")
	}
	if want := "/repos/" + Repo + "/releases/latest"; gotPath != want {
		t.Errorf("path: got %q, want %q", gotPath, want)
	}
	// GitHub answers a request with no User-Agent with 403, so this header
	// failing to arrive would break every check.
	if want := "digicli/v0.1.2"; gotAgent != want {
		t.Errorf("user agent: got %q, want %q", gotAgent, want)
	}
}

// A release with no html_url still has to produce a link to send someone to.
func TestLatestFallsBackToATagURL(t *testing.T) {
	serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"tag_name": "v0.1.3"}`))
	})

	release, err := Latest(context.Background(), "v0.1.2")
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if want := "https://github.com/" + Repo + "/releases/tag/v0.1.3"; release.URL != want {
		t.Errorf("URL: got %q, want %q", release.URL, want)
	}
}

func TestLatestReportsFailures(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
	}{
		{"rate limited", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
		}},
		{"no releases", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}},
		{"server error", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
		}},
		{"not json", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("<html>"))
		}},
		{"no tag", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"html_url": "https://example.com"}`))
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			serve(t, tt.handler)
			if _, err := Latest(context.Background(), "v0.1.2"); err == nil {
				t.Error("expected an error")
			}
		})
	}
}

func TestLatestHonoursACancelledContext(t *testing.T) {
	serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"tag_name": "v0.1.3"}`))
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := Latest(ctx, "v0.1.2"); err == nil {
		t.Error("expected an error for a cancelled check")
	}
}
