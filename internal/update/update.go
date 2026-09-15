// Package update checks GitHub for a newer DigiCLI release and knows how the
// running binary was installed, so /update can run the right upgrade command.
//
// The check is the only request DigiCLI makes that does not go to the user's
// model endpoint, which is why it is opt-in: nothing here runs until the
// config says it may.
package update

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Repo is the GitHub repository releases are published to. It matches REPO in
// the Makefile — the tag pushed there is what every install channel builds on.
const Repo = "10txn/digicli"

// apiBase is a variable rather than a constant so tests can point the check at
// a local server instead of the real API.
var apiBase = "https://api.github.com"

// client bounds the check independently of the caller's context, so a hung
// connection cannot hold a session's startup command open indefinitely.
var client = &http.Client{Timeout: 10 * time.Second}

// Release is a published version of DigiCLI.
type Release struct {
	// Version is the git tag, e.g. "v0.1.3".
	Version string
	// URL is the release page, for an install DigiCLI cannot upgrade itself.
	URL string
}

// releaseResponse is the part of GitHub's release JSON that matters here.
type releaseResponse struct {
	TagName string `json:"tag_name"`
	HTMLURL string `json:"html_url"`
}

// Latest asks GitHub for the newest published release. Prereleases are
// excluded by the endpoint itself, which is what keeps an -rc tag published
// under the npm "next" dist-tag off everybody's status bar.
//
// current is sent as the User-Agent; GitHub answers an unidentified client
// with 403, so it is not optional.
func Latest(ctx context.Context, current string) (Release, error) {
	url := apiBase + "/repos/" + Repo + "/releases/latest"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Release{}, fmt.Errorf("building the update request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "digicli/"+current)

	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return Release{}, fmt.Errorf("the update check timed out")
		}
		return Release{}, fmt.Errorf("could not reach GitHub to check for updates")
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusForbidden, http.StatusTooManyRequests:
		// Unauthenticated callers get 60 requests an hour per address, which
		// a shared address can exhaust without this machine doing anything.
		return Release{}, fmt.Errorf("GitHub is rate-limiting update checks — try again later")
	case http.StatusNotFound:
		return Release{}, fmt.Errorf("no published release found for %s", Repo)
	default:
		return Release{}, fmt.Errorf("GitHub answered %s", resp.Status)
	}

	// The real payload is a few KB of release notes and asset metadata; the
	// limit is protection against a redirect to something much larger.
	var release releaseResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&release); err != nil {
		return Release{}, fmt.Errorf("reading GitHub's answer: %w", err)
	}
	if release.TagName == "" {
		return Release{}, fmt.Errorf("GitHub returned a release with no tag")
	}

	url = release.HTMLURL
	if url == "" {
		url = "https://github.com/" + Repo + "/releases/tag/" + release.TagName
	}
	return Release{Version: release.TagName, URL: url}, nil
}

// version is a parsed release number. Anything that does not look like one —
// the "dev" a plain `go build` stamps, most of all — parses with ok false and
// never compares as older than a release, so an unversioned build is never
// nagged to upgrade.
type version struct {
	major, minor, patch int
	// pre is the prerelease part, empty for a final release.
	pre string
	// dev marks a build made some commits past a tag, which `git describe`
	// renders as v0.1.2-4-gabc1234. Such a build is ahead of that tag, not
	// behind it — the opposite of what semver would make of the suffix.
	dev bool
	ok  bool
}

// Comparable reports whether a version string can be ordered against a release
// tag. It is false for the "dev" of an untagged build.
func Comparable(v string) bool { return parse(v).ok }

// Newer reports whether latest is a release the running build does not have.
func Newer(current, latest string) bool {
	c, l := parse(current), parse(latest)
	if !c.ok || !l.ok {
		return false
	}
	return compare(l, c) > 0
}

func parse(v string) version {
	s := strings.TrimSpace(v)
	s = strings.TrimPrefix(s, "v")
	s = strings.TrimPrefix(s, "V")
	// An uncommitted tree adds a suffix that says nothing about ordering.
	s = strings.TrimSuffix(s, "-dirty")
	// Build metadata is not ordered, as semver has it.
	if i := strings.IndexByte(s, '+'); i >= 0 {
		s = s[:i]
	}
	if s == "" {
		return version{}
	}

	core, pre, _ := strings.Cut(s, "-")
	fields := strings.Split(core, ".")
	if len(fields) != 3 {
		return version{}
	}

	var nums [3]int
	for i, field := range fields {
		n, err := strconv.Atoi(field)
		if err != nil || n < 0 {
			return version{}
		}
		nums[i] = n
	}

	out := version{major: nums[0], minor: nums[1], patch: nums[2], ok: true}
	if pre == "" {
		return out
	}
	// git describe's "<commits>-g<hash>" tail, rather than a real prerelease.
	if commits, hash, found := strings.Cut(pre, "-"); found &&
		digits(commits) && strings.HasPrefix(hash, "g") {
		out.dev = true
		return out
	}
	out.pre = pre
	return out
}

func digits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// compare orders two parsed versions the way semver does, with the dev case
// bolted on: at equal numbers, a prerelease sorts below the release it leads
// to, and a build made past that release sorts above it.
func compare(a, b version) int {
	for _, pair := range [][2]int{
		{a.major, b.major},
		{a.minor, b.minor},
		{a.patch, b.patch},
	} {
		if pair[0] != pair[1] {
			return sign(pair[0] - pair[1])
		}
	}

	if ra, rb := rank(a), rank(b); ra != rb {
		return sign(ra - rb)
	}
	// Two prereleases of the same version: dotted identifiers compare
	// field by field in semver, which for rc.1 vs rc.2 the string order
	// already gets right.
	return sign(strings.Compare(a.pre, b.pre))
}

func rank(v version) int {
	switch {
	case v.dev:
		return 2
	case v.pre == "":
		return 1
	default:
		return 0
	}
}

func sign(n int) int {
	switch {
	case n > 0:
		return 1
	case n < 0:
		return -1
	default:
		return 0
	}
}
