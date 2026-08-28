package diffmode

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/olmesm/planman/internal/gitdiff"
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// Search result caps, so a one-letter-wide query on a huge diff cannot
// produce an unbounded payload.
const (
	minSearchQuery     = 2
	maxSearchMatches   = 2000
	maxMatchesPerFile  = 200
	maxSearchFileCount = 500
)

type searchMatch struct {
	Side string `json:"side"`
	Line int    `json:"line"`
}

type searchFile struct {
	Path        string        `json:"path"`
	Fingerprint string        `json:"fingerprint"`
	Count       int           `json:"count"`
	NameMatch   bool          `json:"name_match"`
	Matches     []searchMatch `json:"matches"`
}

type searchResult struct {
	Query string        `json:"query"`
	Total int           `json:"total"`
	Files []*searchFile `json:"files"`
}

// searchDiff finds case-insensitive substring matches across the current
// diff's rows and file names. Each matching row counts once per
// occurrence but is reported once, anchored like a comment would be.
func searchDiff(d *gitdiff.Diff, query string) *searchResult {
	res := &searchResult{Query: query}
	needle := strings.ToLower(query)
	for _, f := range d.Files {
		if len(res.Files) >= maxSearchFileCount || res.Total >= maxSearchMatches {
			break
		}
		sf := &searchFile{Path: f.Path(), Fingerprint: f.Fingerprint, Matches: []searchMatch{}}
		if strings.Contains(strings.ToLower(f.Path()), needle) ||
			(f.OldPath != "" && strings.Contains(strings.ToLower(f.OldPath), needle)) {
			sf.NameMatch = true
			sf.Count++
			res.Total++
		}
		for _, h := range f.Hunks {
			for _, r := range h.Rows {
				n := strings.Count(strings.ToLower(r.Text), needle)
				if n == 0 {
					continue
				}
				sf.Count += n
				res.Total += n
				if len(sf.Matches) < maxMatchesPerFile {
					side, line := anchorFor(r)
					sf.Matches = append(sf.Matches, searchMatch{Side: side, Line: line})
				}
			}
		}
		if sf.Count > 0 {
			res.Files = append(res.Files, sf)
		}
	}
	return res
}

func (m *Mode) handleSearch(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if len([]rune(query)) < minSearchQuery {
		writeJSON(w, http.StatusOK, &searchResult{Query: query, Files: []*searchFile{}})
		return
	}
	st := m.state()
	d, err := m.repo.DiffRange(gitdiff.RangeOptions{
		Base: st.base, Head: st.head, MergeBase: st.mergeBase, IgnoreWhitespace: st.ignoreWS,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	res := searchDiff(d, query)
	if res.Files == nil {
		res.Files = []*searchFile{}
	}
	writeJSON(w, http.StatusOK, res)
}
