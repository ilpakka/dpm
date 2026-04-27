package search

import (
	_ "embed"

	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

var bundledSnapshot []byte

type Entry struct {
	RepoURL     string    `json:"repo_url"`
	Owner       string    `json:"owner"`
	RepoName    string    `json:"repo_name"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Stars       int       `json:"stars"`
	LastSeen    time.Time `json:"last_seen"`

	Verified bool `json:"verified"`
}

type snapshot struct {
	LastSync time.Time `json:"last_sync"`
	LastFull time.Time `json:"last_full"`
	Entries  []Entry   `json:"entries"`
}

type Index struct {
	mu       sync.RWMutex
	entries  map[string]Entry
	dpmDir   string
	lastSync time.Time
	lastFull time.Time
}

const (
	snapshotFile   = "snapshot.json"
	incrementalTTL = 24 * time.Hour
	fullSyncTTL    = 7 * 24 * time.Hour
	entryTTL       = 30 * 24 * time.Hour
)

func LoadIndex(dpmDir string) (*Index, error) {
	idx := &Index{
		entries: make(map[string]Entry),
		dpmDir:  dpmDir,
	}

	dir := idx.indexDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create index dir: %w", err)
	}

	snapPath := filepath.Join(dir, snapshotFile)
	if _, err := os.Stat(snapPath); os.IsNotExist(err) {
		if err := os.WriteFile(snapPath, bundledSnapshot, 0o600); err != nil {
			log.Printf("search: failed to seed snapshot: %v", err)
		}
	}

	if err := idx.load(); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("load index: %w", err)
	}
	return idx, nil
}

func (idx *Index) indexDir() string {
	return filepath.Join(idx.dpmDir, "index")
}

func (idx *Index) load() error {
	data, err := os.ReadFile(filepath.Join(idx.indexDir(), snapshotFile))
	if err != nil {
		return err
	}
	var snap snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("parse snapshot: %w", err)
	}
	for _, e := range snap.Entries {
		idx.entries[e.RepoURL] = e
	}
	idx.lastSync = snap.LastSync
	idx.lastFull = snap.LastFull
	return nil
}

func (idx *Index) Entries() []Entry {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	out := make([]Entry, 0, len(idx.entries))
	for _, e := range idx.entries {
		out = append(out, e)
	}
	return out
}

func (idx *Index) NeedsIncrementalSync() bool {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return time.Since(idx.lastSync) > incrementalTTL
}

func (idx *Index) NeedsFullSync() bool {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return time.Since(idx.lastFull) > fullSyncTTL
}

func (idx *Index) lastSyncTime() time.Time {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return idx.lastSync
}

func (idx *Index) applyChanges(adds []Entry, updates []Entry, removes []string) error {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	now := time.Now().UTC()

	for i := range adds {
		e := adds[i]
		e.LastSeen = now
		idx.entries[e.RepoURL] = e
	}
	for i := range updates {
		e := updates[i]
		if existing, ok := idx.entries[e.RepoURL]; ok {
			e.Verified = existing.Verified
		}
		e.LastSeen = now
		idx.entries[e.RepoURL] = e
	}
	for _, url := range removes {
		delete(idx.entries, url)
	}

	return idx.save()
}

func (idx *Index) MarkVerified(repoURL string) error {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	e, ok := idx.entries[repoURL]
	if !ok {
		return fmt.Errorf("entry not found: %s", repoURL)
	}
	e.Verified = true
	idx.entries[repoURL] = e
	return idx.save()
}

func (idx *Index) save() error {
	snap := snapshot{
		LastSync: idx.lastSync,
		LastFull: idx.lastFull,
		Entries:  make([]Entry, 0, len(idx.entries)),
	}
	for _, e := range idx.entries {
		snap.Entries = append(snap.Entries, e)
	}
	sort.Slice(snap.Entries, func(i, j int) bool {
		return snap.Entries[i].RepoURL < snap.Entries[j].RepoURL
	})

	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal snapshot: %w", err)
	}

	dir := idx.indexDir()
	tmpFile, err := os.CreateTemp(dir, "snapshot-*.tmp")
	if err != nil {
		return fmt.Errorf("create snapshot temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("write snapshot temp file: %w", err)
	}
	if err := tmpFile.Chmod(0o600); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("chmod snapshot temp file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("close snapshot temp file: %w", err)
	}
	if err := os.Rename(tmpPath, filepath.Join(dir, snapshotFile)); err != nil {
		return fmt.Errorf("rename snapshot: %w", err)
	}
	return nil
}

type Provider interface {
	ID() string
	Search(ctx context.Context, since time.Time) ([]RepoMeta, error)
	RawFileURL(repoURL, path string) string
	ValidateRepoURL(url string) bool
}

type RepoMeta struct {
	URL         string
	Owner       string
	RepoName    string
	Description string
	Stars       int
	UpdatedAt   time.Time
}

var ErrRateLimited = fmt.Errorf("github API rate limit reached — try again later")

type githubSearchResponse struct {
	Items []struct {
		HTMLURL         string    `json:"html_url"`
		Name            string    `json:"name"`
		Description     string    `json:"description"`
		StargazersCount int       `json:"stargazers_count"`
		UpdatedAt       time.Time `json:"updated_at"`
		Owner           struct {
			Login string `json:"login"`
		} `json:"owner"`
	} `json:"items"`
}

var ownerPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9-]{0,37}[a-zA-Z0-9]$|^[a-zA-Z0-9]$`)

var repoNamePattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,100}$`)

func parseGitHubURL(rawURL string) (owner, repo string) {
	const prefix = "https://github.com/"
	if !strings.HasPrefix(rawURL, prefix) {
		return "", ""
	}
	rest := rawURL[len(prefix):]
	slash := strings.IndexByte(rest, '/')
	if slash < 0 || slash == len(rest)-1 {
		return "", ""
	}
	o, r := rest[:slash], rest[slash+1:]
	if strings.ContainsAny(r, "/?#") {
		return "", ""
	}
	if !ownerPattern.MatchString(o) || !repoNamePattern.MatchString(r) {
		return "", ""
	}
	return o, r
}

type GitHubProvider struct {
	client *http.Client
}

func NewGitHubProvider() *GitHubProvider {
	return &GitHubProvider{
		client: &http.Client{
			Timeout: 15 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				host := req.URL.Host
				if host == "raw.githubusercontent.com" || host == "api.github.com" {
					return nil
				}
				return fmt.Errorf("redirect to unexpected host: %s", host)
			},
		},
	}
}

func (g *GitHubProvider) ID() string { return "github" }

func (g *GitHubProvider) ValidateRepoURL(url string) bool {
	owner, repo := parseGitHubURL(url)
	return owner != "" && repo != ""
}

func (g *GitHubProvider) RawFileURL(repoURL, path string) string {
	owner, repo := parseGitHubURL(repoURL)
	if owner == "" {
		return ""
	}
	return fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/main/%s", owner, repo, path)
}

func buildSearchQuery(since time.Time) string {
	q := "topic:dpm-profile"
	if !since.IsZero() {
		q += fmt.Sprintf("+pushed:>%s", since.UTC().Format("2006-01-02"))
	}
	return q
}

func buildGitHubSearchURL(q string, perPage, page int) string {
	params := url.Values{}
	params.Set("q", q)
	params.Set("sort", "updated")
	params.Set("per_page", fmt.Sprintf("%d", perPage))
	params.Set("page", fmt.Sprintf("%d", page))
	return "https://api.github.com/search/repositories?" + params.Encode()
}

func newGitHubSearchRequest(ctx context.Context, apiURL string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "dpm-search-index/1.0")
	return req, nil
}

func mapGitHubItems(items []struct {
	HTMLURL         string    `json:"html_url"`
	Name            string    `json:"name"`
	Description     string    `json:"description"`
	StargazersCount int       `json:"stargazers_count"`
	UpdatedAt       time.Time `json:"updated_at"`
	Owner           struct {
		Login string `json:"login"`
	} `json:"owner"`
}) []RepoMeta {
	out := make([]RepoMeta, 0, len(items))
	for _, item := range items {
		out = append(out, RepoMeta{
			URL:         item.HTMLURL,
			Owner:       item.Owner.Login,
			RepoName:    item.Name,
			Description: item.Description,
			Stars:       item.StargazersCount,
			UpdatedAt:   item.UpdatedAt,
		})
	}
	return out
}

func (g *GitHubProvider) searchPage(ctx context.Context, q string, perPage, page int) (repos []RepoMeta, hasMore bool, rateLimited bool, err error) {
	apiURL := buildGitHubSearchURL(q, perPage, page)
	req, err := newGitHubSearchRequest(ctx, apiURL)
	if err != nil {
		return nil, false, false, fmt.Errorf("build request: %w", err)
	}

	resp, err := g.client.Do(req)
	if err != nil {
		return nil, false, false, fmt.Errorf("github search: %w", err)
	}
	defer resp.Body.Close()

	remaining := resp.Header.Get("X-RateLimit-Remaining")
	lowRemaining := remaining == "0" || remaining == "1" || remaining == "2"
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == 429 {
		return nil, false, true, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, false, false, fmt.Errorf("github search: HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, false, false, fmt.Errorf("read github response: %w", err)
	}

	var parsed githubSearchResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, false, false, fmt.Errorf("parse github response: %w", err)
	}

	repos = mapGitHubItems(parsed.Items)
	hasMore = len(parsed.Items) >= perPage
	rateLimited = lowRemaining
	return repos, hasMore, rateLimited, nil
}

func (g *GitHubProvider) Search(ctx context.Context, since time.Time) ([]RepoMeta, error) {
	const perPage = 100
	const maxPages = 3

	q := buildSearchQuery(since)

	var results []RepoMeta
	for page := 1; page <= maxPages; page++ {
		pageResults, hasMore, rateLimited, err := g.searchPage(ctx, q, perPage, page)
		if err != nil {
			return results, err
		}
		if rateLimited {
			if len(results) > 0 || len(pageResults) > 0 {
				results = append(results, pageResults...)
				return results, ErrRateLimited
			}
			return nil, ErrRateLimited
		}

		results = append(results, pageResults...)
		if !hasMore {
			break
		}
	}
	return results, nil
}

func computeFetchedEntryDelta(provider Provider, entries map[string]Entry, fetched []RepoMeta, now time.Time) (adds, updates []Entry, fetchedByURL map[string]struct{}) {
	fetchedByURL = make(map[string]struct{}, len(fetched))
	for _, meta := range fetched {
		fetchedByURL[meta.URL] = struct{}{}
		if !provider.ValidateRepoURL(meta.URL) {
			continue
		}

		incoming := Entry{
			RepoURL:     meta.URL,
			Owner:       meta.Owner,
			RepoName:    meta.RepoName,
			Name:        meta.RepoName,
			Description: meta.Description,
			Stars:       meta.Stars,
			LastSeen:    now,
		}

		existing, exists := entries[meta.URL]
		if !exists {
			adds = append(adds, incoming)
			continue
		}

		if existing.Stars != incoming.Stars ||
			existing.Description != incoming.Description ||
			existing.RepoName != incoming.RepoName {
			incoming.Verified = existing.Verified
			incoming.Name = existing.Name
			updates = append(updates, incoming)
			continue
		}

		existing.LastSeen = now
		entries[meta.URL] = existing
	}
	return adds, updates, fetchedByURL
}

func collectStaleURLs(entries map[string]Entry, fetchedByURL map[string]struct{}, now time.Time) []string {
	var removes []string
	for url, e := range entries {
		if _, seen := fetchedByURL[url]; seen {
			continue
		}
		if now.Sub(e.LastSeen) > entryTTL {
			removes = append(removes, url)
		}
	}
	return removes
}

type SyncResult struct {
	Added   int
	Updated int
	Removed int
}

func Sync(ctx context.Context, idx *Index, provider Provider) (SyncResult, error) {
	isFull := idx.NeedsFullSync()
	var since time.Time
	if !isFull {
		since = idx.lastSyncTime()
	}

	fetched, err := provider.Search(ctx, since)
	rateLimited := errors.Is(err, ErrRateLimited)
	if err != nil && !rateLimited {
		return SyncResult{}, fmt.Errorf("provider search: %w", err)
	}

	idx.mu.Lock()
	now := time.Now().UTC()

	adds, updates, fetchedByURL := computeFetchedEntryDelta(provider, idx.entries, fetched, now)
	var removes []string

	if isFull {
		removes = collectStaleURLs(idx.entries, fetchedByURL, now)
		idx.lastFull = now
	}
	idx.lastSync = now
	idx.mu.Unlock()

	if err := idx.applyChanges(adds, updates, removes); err != nil {
		return SyncResult{}, fmt.Errorf("apply changes: %w", err)
	}

	result := SyncResult{Added: len(adds), Updated: len(updates), Removed: len(removes)}
	if rateLimited {
		return result, ErrRateLimited
	}
	return result, nil
}
