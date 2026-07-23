package releasecheck

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const githubAPIVersion = "2026-03-10"

type Info struct {
	TagName string
	URL     string
}

type Source interface {
	Latest(context.Context) (Info, error)
}

type GitHub struct {
	Client   *http.Client
	Endpoint string
}

func NewGitHub(repository string) *GitHub {
	return &GitHub{
		Client:   &http.Client{Timeout: 5 * time.Second},
		Endpoint: "https://api.github.com/repos/" + repository + "/releases/latest",
	}
}

func (g *GitHub) Latest(ctx context.Context) (Info, error) {
	if g == nil || g.Endpoint == "" {
		return Info{}, errors.New("release source is not configured")
	}
	client := g.Client
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.Endpoint, nil)
	if err != nil {
		return Info{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "sbm-panel")
	req.Header.Set("X-GitHub-Api-Version", githubAPIVersion)
	response, err := client.Do(req)
	if err != nil {
		return Info{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return Info{}, fmt.Errorf("GitHub returned %s", response.Status)
	}
	var payload struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&payload); err != nil {
		return Info{}, fmt.Errorf("decode GitHub release: %w", err)
	}
	releaseURL, err := url.Parse(payload.HTMLURL)
	if err != nil || releaseURL.Scheme != "https" || releaseURL.Host != "github.com" {
		return Info{}, errors.New("GitHub release URL is invalid")
	}
	if _, ok := parseVersion(payload.TagName); !ok {
		return Info{}, errors.New("GitHub release tag is not a semantic version")
	}
	return Info{TagName: payload.TagName, URL: payload.HTMLURL}, nil
}

type version struct {
	major, minor, patch int
	prerelease          string
}

func parseVersion(value string) (version, bool) {
	value = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(value), "v"))
	value = strings.SplitN(value, "+", 2)[0]
	parts := strings.SplitN(value, "-", 2)
	numbers := strings.Split(parts[0], ".")
	if len(numbers) != 3 {
		return version{}, false
	}
	parsed := version{}
	values := []*int{&parsed.major, &parsed.minor, &parsed.patch}
	for i, number := range numbers {
		n, err := strconv.Atoi(number)
		if err != nil || n < 0 {
			return version{}, false
		}
		*values[i] = n
	}
	if len(parts) == 2 {
		parsed.prerelease = parts[1]
	}
	return parsed, true
}

func IsNewer(latest, current string) bool {
	next, nextOK := parseVersion(latest)
	installed, installedOK := parseVersion(current)
	if !nextOK || !installedOK {
		return false
	}
	for _, pair := range [][2]int{{next.major, installed.major}, {next.minor, installed.minor}, {next.patch, installed.patch}} {
		if pair[0] != pair[1] {
			return pair[0] > pair[1]
		}
	}
	return installed.prerelease != "" && next.prerelease == ""
}
