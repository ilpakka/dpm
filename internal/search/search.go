package search

import (
	"context"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"dpm.iskff.fi/dpm/internal/catalog"
	"dpm.iskff.fi/dpm/internal/dotfiles"
	"dpm.iskff.fi/dpm/internal/profiles"
	"dpm.iskff.fi/dpm/internal/util"
)

type SearchReadyMsg struct {
	Generation int
	Query      string
}

func DebounceCmd(delay time.Duration, generation int, query string) tea.Cmd {
	return func() tea.Msg {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		<-timer.C
		return SearchReadyMsg{Generation: generation, Query: query}
	}
}

const scoreThreshold = 0.15

func score(candidate, query string) float64 {
	if query == "" {
		return 1.0
	}
	c := strings.ToLower(candidate)
	q := strings.ToLower(query)

	if c == q {
		return 1.0
	}
	if strings.HasPrefix(c, q) {
		return 0.9
	}
	if strings.Contains(c, q) {
		return 0.8
	}

	sim := util.TrigramSimilarity(c, q)
	switch {
	case sim >= 0.5:
		return 0.7
	case sim >= 0.3:
		return 0.5
	case sim >= 0.15:
		return 0.3
	default:
		return 0.0
	}
}

func scoreItem(name, description string, tags []string, query string) float64 {
	best := score(name, query)
	if s := score(description, query) * 0.7; s > best {
		best = s
	}
	for _, tag := range tags {
		if s := score(tag, query) * 0.5; s > best {
			best = s
		}
	}
	return best
}

func rank[T any](
	items []T,
	query string,
	fields func(T) (name, desc string, tags []string),
) []T {
	if query == "" {
		out := make([]T, len(items))
		copy(out, items)
		return out
	}

	type scored struct {
		item  T
		score float64
	}

	results := make([]scored, 0, len(items))
	for _, item := range items {
		name, desc, tags := fields(item)
		s := scoreItem(name, desc, tags, query)
		if s >= scoreThreshold {
			results = append(results, scored{item, s})
		}
	}

	sort.SliceStable(results, func(i, j int) bool {
		return results[i].score > results[j].score
	})

	out := make([]T, len(results))
	for i, r := range results {
		out[i] = r.item
	}
	return out
}

func RankTools(tools []catalog.Tool, query string) []catalog.Tool {
	return rank(tools, query,
		func(t catalog.Tool) (string, string, []string) {
			return t.Name, t.Description, append([]string{t.ID}, t.Tags...)
		},
	)
}

func RankDotfiles(dfs []dotfiles.Dotfile, query string) []dotfiles.Dotfile {
	return rank(dfs, query,
		func(d dotfiles.Dotfile) (string, string, []string) {
			return d.Name, d.Description, []string{d.ID, d.ToolID}
		},
	)
}

func RankProfiles(ps []profiles.Profile, query string) []profiles.Profile {
	return rank(ps, query,
		func(p profiles.Profile) (string, string, []string) {
			return p.Name, p.Description, []string{p.ID, p.Category}
		},
	)
}

type SyncDoneMsg struct {
	Result   SyncResult
	FullSync bool
}

type SyncErrMsg struct {
	Err error
}

func SyncCmd(idx *Index, provider Provider) tea.Cmd {
	needsFull := idx.NeedsFullSync()
	if !needsFull && !idx.NeedsIncrementalSync() {
		return nil
	}
	return func() tea.Msg {
		isFull := needsFull
		result, err := Sync(context.Background(), idx, provider)
		if err != nil {
			return SyncErrMsg{Err: err}
		}
		return SyncDoneMsg{Result: result, FullSync: isFull}
	}
}

func RankBundles(entries []Entry, query string) []Entry {
	ranked := rank(entries, query,
		func(e Entry) (string, string, []string) {
			return e.Name, e.Description, []string{e.Owner, e.RepoName}
		},
	)
	if query == "" {
		return ranked
	}
	type bonused struct {
		entry Entry
		score float64
	}
	bs := make([]bonused, len(ranked))
	for i, e := range ranked {
		s := scoreItem(e.Name, e.Description, []string{e.Owner, e.RepoName}, query)
		if e.Stars > 0 {
			s += float64(e.Stars) / 1e9
		}
		bs[i] = bonused{e, s}
	}
	sort.SliceStable(bs, func(i, j int) bool {
		return bs[i].score > bs[j].score
	})
	out := make([]Entry, len(bs))
	for i, b := range bs {
		out[i] = b.entry
	}
	return out
}
