// cmd/sync-index syncs the community profile index and optionally tests
// fetching a specific profile bundle.
//
// # Sync the index and write data/snapshot.json (run before tagging a release):
//
//	go run ./cmd/sync-index/
//
// # Write the snapshot to a custom path:
//
//	go run ./cmd/sync-index/ -out /path/to/snapshot.json
//
// # Test fetching a specific profile repo live (no index write):
//
//	go run ./cmd/sync-index/ -fetch https://github.com/user/dpm-profile-repo
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"dpm.iskff.fi/dpm/internal/catalog"
	"dpm.iskff.fi/dpm/internal/search"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "sync-index: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// Default output path: relative to cwd, works when run from repo root.
	defaultOut := filepath.Join("internal", "search", "data", "snapshot.json")

	out := flag.String("out", defaultOut, "path to write snapshot.json")
	fetchURL := flag.String("fetch", "", "test-fetch a profile bundle from this GitHub repo URL")
	flag.Parse()

	if *fetchURL != "" {
		return runFetch(*fetchURL)
	}
	return runSync(*out)
}

// runSync performs a full sync and writes the snapshot to outPath.
func runSync(outPath string) error {
	fmt.Println("Syncing community profile index from GitHub...")

	tmpDir, err := os.MkdirTemp("", "dpm-sync-index-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	idx, err := search.LoadIndex(tmpDir)
	if err != nil {
		return fmt.Errorf("load index: %w", err)
	}

	provider := search.NewGitHubProvider()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	result, err := search.Sync(ctx, idx, provider)
	if err != nil {
		if errors.Is(err, search.ErrRateLimited) {
			fmt.Fprintf(os.Stderr, "WARNING: %v\n", err)
			fmt.Fprintf(os.Stderr, "Partial results (%d added) will be written. Run again later for a complete snapshot.\n", result.Added)
		} else {
			return fmt.Errorf("sync: %w", err)
		}
	}

	fmt.Printf("Sync complete: %d added, %d updated, %d removed\n", result.Added, result.Updated, result.Removed)

	snapData, err := os.ReadFile(filepath.Join(tmpDir, "index", "snapshot.json"))
	if err != nil {
		return fmt.Errorf("read compacted snapshot: %w", err)
	}

	// Pretty-print for readable git diffs.
	var pretty interface{}
	if err := json.Unmarshal(snapData, &pretty); err != nil {
		return fmt.Errorf("parse snapshot: %w", err)
	}
	formatted, err := json.MarshalIndent(pretty, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal snapshot: %w", err)
	}
	formatted = append(formatted, '\n')

	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}
	if err := os.WriteFile(outPath, formatted, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", outPath, err)
	}

	fmt.Printf("Written to %s\n", outPath)
	fmt.Println("Commit this file and tag the release.")
	return nil
}

// runFetch fetches and validates a profile bundle from a real GitHub repo,
// then attempts to fetch each dotfile listed in the bundle.
// No files are written to disk — output goes to stdout.
func runFetch(repoURL string) error {
	fmt.Printf("Fetching profile bundle from %s\n\n", repoURL)

	provider := search.NewGitHubProvider()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Build known tool IDs from the local catalog directory when available.
	tools, err := catalog.LoadCatalog("catalog")
	if err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: could not load catalog directory: %v\n", err)
		tools = catalog.GetMockTools()
	}
	knownIDs := catalog.ToolIDSet(tools)

	bundle, err := search.Fetch(ctx, provider, repoURL, knownIDs)
	if err != nil {
		return fmt.Errorf("fetch profile.yaml: %w", err)
	}

	// Print the parsed bundle as JSON.
	out, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal bundle: %w", err)
	}
	fmt.Println("--- profile.yaml ---")
	fmt.Println(string(out))

	if len(bundle.Dotfiles) == 0 {
		fmt.Println("\nNo dotfiles listed in profile.")
		return nil
	}

	// Attempt to fetch each dotfile and print the first 200 bytes.
	fmt.Printf("\n--- dotfiles (%d) ---\n", len(bundle.Dotfiles))
	for _, name := range bundle.Dotfiles {
		data, err := search.FetchDotfile(ctx, provider, repoURL, name)
		if err != nil {
			fmt.Printf("  %-30s  ERROR: %v\n", name, err)
			continue
		}
		preview := data
		truncated := false
		if len(preview) > 200 {
			preview = preview[:200]
			truncated = true
		}
		fmt.Printf("  %-30s  %d bytes OK\n", name, len(data))
		fmt.Printf("    %q", string(preview))
		if truncated {
			fmt.Print(" ...")
		}
		fmt.Println()
	}

	return nil
}
