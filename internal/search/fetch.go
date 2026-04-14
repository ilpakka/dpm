package search

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"path"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

type ProfileBundle struct {
	ID          string `yaml:"id"`
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Version     string `yaml:"version"`

	Tools []string `yaml:"tools"`

	Dotfiles   []string `yaml:"dotfiles"`
	SourceRepo string   `yaml:"-"`
}

var fetchClient = &http.Client{
	Timeout: 10 * time.Second,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if req.URL.Host == "raw.githubusercontent.com" {
			return nil
		}
		return fmt.Errorf("redirect to unexpected host %q blocked", req.URL.Host)
	},
}

func Fetch(ctx context.Context, provider Provider, repoURL string, knownToolIDs map[string]struct{}) (*ProfileBundle, error) {
	if !provider.ValidateRepoURL(repoURL) {
		return nil, fmt.Errorf("invalid or unsupported repo URL: %q", repoURL)
	}

	rawURL := provider.RawFileURL(repoURL, "profile.yaml")
	if rawURL == "" {
		return nil, fmt.Errorf("provider could not construct raw URL for %q", repoURL)
	}

	data, err := fetchRaw(ctx, rawURL, 64*1024)
	if err != nil {
		return nil, fmt.Errorf("fetch profile.yaml from %q: %w", repoURL, err)
	}

	var bundle ProfileBundle
	if err := yaml.Unmarshal(data, &bundle); err != nil {
		return nil, fmt.Errorf("parse profile.yaml from %q: %w", repoURL, err)
	}

	if err := validateBundle(&bundle); err != nil {
		return nil, fmt.Errorf("invalid profile.yaml from %q: %w", repoURL, err)
	}

	bundle.Tools = filterAndDedupeKnownTools(bundle.Tools, knownToolIDs)
	bundle.Dotfiles = sanitizeAndDedupeDotfiles(bundle.Dotfiles)
	bundle.SourceRepo = repoURL

	return &bundle, nil
}

func filterAndDedupeKnownTools(tools []string, knownToolIDs map[string]struct{}) []string {
	if len(tools) == 0 {
		return tools
	}

	seen := make(map[string]struct{}, len(tools))
	out := tools[:0]
	for _, id := range tools {
		if len(knownToolIDs) > 0 {
			if _, ok := knownToolIDs[id]; !ok {
				continue
			}
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func sanitizeAndDedupeDotfiles(dotfiles []string) []string {
	if len(dotfiles) == 0 {
		return dotfiles
	}

	seen := make(map[string]struct{}, len(dotfiles))
	out := dotfiles[:0]
	for _, name := range dotfiles {
		clean, ok := sanitizeFilename(name)
		if !ok {
			continue
		}
		if _, exists := seen[clean]; exists {
			continue
		}
		seen[clean] = struct{}{}
		out = append(out, clean)
	}
	return out
}

func FetchDotfile(ctx context.Context, provider Provider, repoURL, filename string) ([]byte, error) {
	if !provider.ValidateRepoURL(repoURL) {
		return nil, fmt.Errorf("invalid repo URL: %q", repoURL)
	}

	clean, ok := sanitizeFilename(filename)
	if !ok {
		return nil, fmt.Errorf("unsafe dotfile filename: %q", filename)
	}

	rawURL := provider.RawFileURL(repoURL, "dotfiles/"+clean)
	if rawURL == "" {
		return nil, fmt.Errorf("provider could not construct raw URL")
	}

	data, err := fetchRaw(ctx, rawURL, 1<<20)
	if err != nil {
		return nil, fmt.Errorf("fetch dotfile %q from %q: %w", clean, repoURL, err)
	}

	if safe, reason := isSafeConfig(data); !safe {
		return nil, fmt.Errorf("dotfile %q from %q rejected: %s", clean, repoURL, reason)
	}

	return data, nil
}

func fetchRaw(ctx context.Context, url string, maxBytes int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "dpm-search-fetch/1.0")

	resp, err := fetchClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("not found (HTTP 404)")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected HTTP status %d", resp.StatusCode)
	}
	if resp.ContentLength > maxBytes && resp.ContentLength != -1 {
		return nil, fmt.Errorf("response exceeds %d-byte limit", maxBytes)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("response exceeds %d-byte limit", maxBytes)
	}
	return data, nil
}

var slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}[a-z0-9]$|^[a-z0-9]$`)

var namePattern = regexp.MustCompile(`^[A-Za-z0-9 _\-]{1,100}$`)

var descPattern = regexp.MustCompile(`^[\x20-\x7E]{0,300}$`)

var versionPattern = regexp.MustCompile(`^\d+\.\d+(\.\d+)?$`)

func validateBundle(b *ProfileBundle) error {

	if b.ID == "" {
		return fmt.Errorf("missing required field 'id'")
	}
	if !slugPattern.MatchString(b.ID) {
		return fmt.Errorf("'id' contains disallowed characters or format (got %q); only lowercase letters, digits, and hyphens are allowed", b.ID)
	}

	if b.Name == "" {
		return fmt.Errorf("missing required field 'name'")
	}
	if !namePattern.MatchString(b.Name) {
		return fmt.Errorf("'name' contains disallowed characters (got %q); only letters, digits, spaces, underscores, and hyphens are allowed", b.Name)
	}

	if b.Description != "" && !descPattern.MatchString(b.Description) {
		b.Description = ""
	}

	if b.Version != "" && !versionPattern.MatchString(b.Version) {
		b.Version = ""
	}

	valid := b.Tools[:0]
	for _, id := range b.Tools {
		if slugPattern.MatchString(id) {
			valid = append(valid, id)
		}
	}
	b.Tools = valid

	return nil
}

var safeFilenamePattern = regexp.MustCompile(`^[a-zA-Z0-9._/\-]+$`)

func sanitizeFilename(name string) (string, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", false
	}

	name = strings.TrimLeft(name, "/")
	if name == "" {
		return "", false
	}

	if strings.Contains(name, "..") {
		return "", false
	}
	name = path.Clean(name)
	if name == "." || name == "" || strings.HasPrefix(name, "../") || name == ".." {
		return "", false
	}

	if name == "" {
		return "", false
	}

	if !safeFilenamePattern.MatchString(name) {
		return "", false
	}
	return name, true
}

func isSafeConfig(content []byte) (bool, string) {

	if !utf8.Valid(content) {
		return false, "content is not valid UTF-8"
	}
	for i, b := range content {

		if b == 0x09 || b == 0x0A || b == 0x0D {
			continue
		}
		if b >= 0x20 && b <= 0x7E {
			continue
		}
		return false, fmt.Sprintf("non-ASCII byte 0x%02X at offset %d (only printable ASCII is accepted)", b, i)
	}

	lower := bytes.ToLower(content)
	for _, pattern := range executionPatterns {
		if bytes.Contains(lower, pattern) {
			return false, fmt.Sprintf("contains disallowed pattern %q", string(pattern))
		}
	}
	return true, ""
}

var executionPatterns = [][]byte{

	[]byte("#!/"),

	[]byte("curl |"),
	[]byte("curl|"),
	[]byte("wget |"),
	[]byte("wget|"),

	[]byte("$(curl"),
	[]byte("$(wget"),
	[]byte("$(python"),
	[]byte("$(perl"),
	[]byte("$(ruby"),

	[]byte("eval $("),
	[]byte("eval $`"),
	[]byte("eval `"),

	[]byte("`base64 -d"),
	[]byte("base64 -d |"),

	[]byte("python -c"),
	[]byte("python3 -c"),
	[]byte("perl -e"),
	[]byte("ruby -e"),
	[]byte("node -e"),
	[]byte("nodejs -e"),
	[]byte("deno eval"),
	[]byte("deno run"),

	[]byte("-----begin"),

	[]byte("ghp_"),
	[]byte("gho_"),
	[]byte("github_pat_"),

	[]byte("github_token="),
	[]byte("gh_token="),
	[]byte("gitlab_token="),
	[]byte("gitlab_ci_token="),
	[]byte("aws_secret_access_key="),
	[]byte("aws_access_key_id="),
}
