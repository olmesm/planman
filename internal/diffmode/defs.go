package diffmode

// Lightweight definition navigation, ported from codiff: Mod+click an
// identifier in the diff and a bounded git-grep plus a conservative
// per-language declaration classifier propose likely definitions — no
// language server involved. False negatives are fine; false positives
// would make navigation feel untrustworthy.

import (
	"fmt"
	"net/http"
	"path"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/olmesm/planman/internal/gitdiff"
	"github.com/olmesm/planman/internal/review"
)

const maxDefCandidates = 12

// extensionGroups: a definition for a file in one group is searched
// across the whole group (headers with sources, JS with TS).
var extensionGroups = [][]string{
	{".cjs", ".cts", ".js", ".jsx", ".mjs", ".mts", ".ts", ".tsx"},
	{".py", ".pyi"},
	{".go"},
	{".rs"},
	{".java", ".kt", ".kts"},
	{".c", ".cc", ".cpp", ".cxx", ".h", ".hh", ".hpp", ".hxx", ".m", ".mm"},
	{".cs"},
	{".rb"},
	{".swift"},
	{".php"},
	{".bash", ".fish", ".sh", ".zsh"},
}

func defPathspecs(p string) []string {
	ext := strings.ToLower(path.Ext(p))
	if ext == "" {
		return nil
	}
	for _, group := range extensionGroups {
		for _, e := range group {
			if e == ext {
				specs := make([]string, len(group))
				for i, g := range group {
					specs[i] = "*" + g
				}
				return specs
			}
		}
	}
	return []string{"*" + ext}
}

// commonKeywords are never useful navigation targets in any of the
// supported languages.
var commonKeywords = map[string]bool{
	"abstract": true, "as": true, "async": true, "await": true, "break": true,
	"case": true, "catch": true, "class": true, "const": true, "continue": true,
	"def": true, "default": true, "defer": true, "do": true, "else": true,
	"enum": true, "export": true, "extends": true, "false": true, "finally": true,
	"fn": true, "for": true, "from": true, "func": true, "function": true,
	"if": true, "implements": true, "import": true, "in": true, "interface": true,
	"let": true, "match": true, "mod": true, "namespace": true, "new": true,
	"nil": true, "none": true, "null": true, "package": true, "pass": true,
	"private": true, "protected": true, "pub": true, "public": true,
	"return": true, "self": true, "static": true, "struct": true, "super": true,
	"switch": true, "this": true, "throw": true, "throws": true, "trait": true,
	"true": true, "try": true, "type": true, "typeof": true, "undefined": true,
	"use": true, "var": true, "void": true, "while": true, "with": true, "yield": true,
}

func isIdentRune(r rune) bool {
	return r == '$' || r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

// validIdentifier accepts what could plausibly be a code identifier.
func validIdentifier(s string) bool {
	if s == "" || len(s) > 256 || commonKeywords[strings.ToLower(s)] {
		return false
	}
	for i, r := range s {
		if i == 0 && unicode.IsDigit(r) {
			return false
		}
		if !isIdentRune(r) {
			return false
		}
	}
	return true
}

// containsIdentifier reports a word-boundary occurrence of ident in line.
func containsIdentifier(ident, line string) bool {
	for from := 0; ; {
		i := strings.Index(line[from:], ident)
		if i < 0 {
			return false
		}
		i += from
		beforeOK := i == 0 || !isIdentRune(rune(line[i-1]))
		end := i + len(ident)
		afterOK := end >= len(line) || !isIdentRune(rune(line[end]))
		if beforeOK && afterOK {
			return true
		}
		from = end
	}
}

type defClass struct {
	kind     string
	strength int
}

type classRule struct {
	exts    map[string]bool
	pattern string // %s is the escaped identifier
	kind    string
	// anchored rules are matched as written (they carry ^\s*); others
	// get word-boundary semantics from the pattern itself
	strength int
}

func extSet(exts ...string) map[string]bool {
	m := map[string]bool{}
	for _, e := range exts {
		m[e] = true
	}
	return m
}

var jsExts = extSet(".cjs", ".cts", ".js", ".jsx", ".mjs", ".mts", ".ts", ".tsx")
var cLikeExts = extSet(".c", ".cc", ".cpp", ".cxx", ".cs", ".h", ".hh", ".hpp", ".hxx", ".java", ".m", ".mm")

var classRules = []classRule{
	{jsExts, `\b(?:async\s+)?function\s+%s\b`, "function", 100},
	{jsExts, `\bclass\s+%s\b`, "class", 100},
	{jsExts, `\binterface\s+%s\b`, "interface", 100},
	{jsExts, `\b(?:type|enum|namespace)\s+%s\b`, "type", 95},
	{jsExts, `\b(?:const|let|var)\s+%s\b`, "variable", 85},

	{extSet(".py", ".pyi"), `^\s*(?:async\s+)?def\s+%s\b`, "function", 100},
	{extSet(".py", ".pyi"), `^\s*class\s+%s\b`, "class", 100},
	{extSet(".py", ".pyi"), `^\s*%s\s*(?::[^=]+)?=`, "variable", 80},

	{extSet(".go"), `^\s*func\s+(?:\([^)]*\)\s*)?%s\b`, "function", 100},
	{extSet(".go"), `^\s*(?:type|var|const)\s+%s\b`, "declaration", 90},

	{extSet(".rs"), `^\s*(?:pub(?:\([^)]*\))?\s+)?(?:async\s+)?fn\s+%s\b`, "function", 100},
	{extSet(".rs"), `^\s*(?:pub(?:\([^)]*\))?\s+)?(?:struct|enum|trait|type|mod|const|static)\s+%s\b`, "declaration", 95},
	{extSet(".rs"), `^\s*macro_rules!\s+%s\b`, "macro", 100},

	{extSet(".rb"), `^\s*def\s+(?:self\.)?%s\b`, "method", 100},
	{extSet(".rb"), `^\s*(?:class|module)\s+%s\b`, "declaration", 95},

	{extSet(".swift"), `^\s*(?:[\w@]+\s+)*func\s+%s\b`, "function", 100},
	{extSet(".swift"), `^\s*(?:[\w@]+\s+)*(?:class|struct|enum|protocol|typealias|let|var)\s+%s\b`, "declaration", 90},

	{extSet(".kt", ".kts"), `^\s*(?:[\w@]+\s+)*fun\s+%s\b`, "function", 100},
	{extSet(".kt", ".kts"), `^\s*(?:[\w@]+\s+)*(?:class|interface|object|typealias|val|var)\s+%s\b`, "declaration", 90},

	{extSet(".php"), `\bfunction\s+%s\b`, "function", 100},
	{extSet(".php"), `\b(?:class|interface|trait|enum)\s+%s\b`, "declaration", 95},

	{extSet(".bash", ".fish", ".sh", ".zsh"), `^\s*(?:function\s+)?%s\s*\(\)`, "function", 100},

	{cLikeExts, `\b(?:class|interface|enum|struct|record|typedef)\s+%s\b`, "type", 100},
	{cLikeExts, `^\s*(?:[\w:<>,*&?\[\]@]+\s+)+%s\s*\(`, "function", 85},
}

// classifyDefinition decides whether a matched line declares ident.
func classifyDefinition(ident, filePath, line string) *defClass {
	ext := strings.ToLower(path.Ext(filePath))
	name := regexp.QuoteMeta(ident)
	for _, rule := range classRules {
		if !rule.exts[ext] {
			continue
		}
		re, err := regexp.Compile(fmt.Sprintf(rule.pattern, name))
		if err != nil {
			continue
		}
		if re.MatchString(line) {
			return &defClass{kind: rule.kind, strength: rule.strength}
		}
	}
	return nil
}

type defCandidate struct {
	Path       string `json:"path"`
	LineNumber int    `json:"line_number"`
	Line       string `json:"line"`
	Kind       string `json:"kind"`
	InDiff     bool   `json:"in_diff"` // the file has a card on the page
	score      int
}

// defSnapshot picks the grep snapshot for the requested side of the
// current comparison.
func (m *Mode) defSnapshot(side string) (revision string, cached, untracked bool, err error) {
	st := m.state()
	if side == string(review.SideOld) {
		_, _, baseSHA, _ := m.rangeRef()
		if baseSHA == "" {
			return "", false, false, fmt.Errorf("no base commit to search")
		}
		return baseSHA, false, false, nil
	}
	switch st.head {
	case gitdiff.Worktree:
		return "", false, true, nil
	case gitdiff.Index:
		return "", true, false, nil
	default:
		sha, err := m.repo.ResolveSHA(st.head)
		if err != nil || sha == "" {
			return "", false, false, fmt.Errorf("cannot resolve head")
		}
		return sha, false, false, nil
	}
}

func (m *Mode) handleDefs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	ident := q.Get("ident")
	file := q.Get("file")
	side := q.Get("side")
	if side != string(review.SideOld) {
		side = string(review.SideNew)
	}
	if !validIdentifier(ident) || file == "" {
		writeJSON(w, http.StatusOK, map[string]any{
			"status": "unavailable", "reason": "select a valid identifier",
		})
		return
	}
	pathspecs := defPathspecs(file)
	if len(pathspecs) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{
			"status": "ready", "identifier": ident, "candidates": []defCandidate{},
		})
		return
	}
	revision, cached, untracked, err := m.defSnapshot(side)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"status": "unavailable", "reason": err.Error()})
		return
	}
	matches, err := m.repo.BoundedGrep(ident, revision, cached, untracked, pathspecs)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"status": "unavailable", "reason": err.Error()})
		return
	}

	// Which files are on the page, so the client knows click-to-jump works.
	inDiff := map[string]bool{}
	if d, err := m.currentDiff(m.state().ignoreWS); err == nil {
		for _, f := range d.Files {
			inDiff[f.Path()] = true
		}
	}

	curDir := path.Dir(file)
	var candidates []defCandidate
	for _, match := range matches {
		if !containsIdentifier(ident, match.Text) {
			continue
		}
		class := classifyDefinition(ident, match.Path, match.Text)
		if class == nil {
			continue
		}
		score := class.strength
		if match.Path == file {
			score += 25
		} else if path.Dir(match.Path) == curDir {
			score += 10
		}
		candidates = append(candidates, defCandidate{
			Path:       match.Path,
			LineNumber: match.Line,
			Line:       strings.TrimSpace(match.Text),
			Kind:       class.kind,
			InDiff:     inDiff[match.Path],
			score:      score,
		})
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if a.score != b.score {
			return a.score > b.score
		}
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.LineNumber < b.LineNumber
	})
	seen := map[string]bool{}
	unique := []defCandidate{}
	for _, c := range candidates {
		key := fmt.Sprintf("%s:%d", c.Path, c.LineNumber)
		if seen[key] {
			continue
		}
		seen[key] = true
		unique = append(unique, c)
		if len(unique) == maxDefCandidates {
			break
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ready", "identifier": ident, "candidates": unique,
	})
}
