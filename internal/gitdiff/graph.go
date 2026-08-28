package gitdiff

import (
	"strconv"
	"strings"
	"time"
)

// Commit is one entry in the history log.
type Commit struct {
	SHA     string
	Short   string
	Parents []string
	Author  string
	When    time.Time
	Refs    []string // branch/tag decorations ("main", "origin/main", "tag: v0.2.0")
	Subject string
	IsHead  bool // current HEAD
}

// GraphRow is a commit with its lane geometry for drawing a commit
// graph. Lanes are column indices; edges are drawn per row: Pass runs
// top-to-bottom for lanes continuing past this commit, Merge runs from
// a lane's top into this commit's dot (a branch ending here), and Fork
// runs from the dot down to a parent's lane in the next row.
type GraphRow struct {
	Commit
	Dot    int
	NLanes int
	IsTip  bool     // no lane above was expecting this commit
	Pass   [][2]int // [laneBefore, laneAfter]
	Merge  []int    // lanesBefore that end at this commit's dot
	Fork   []int    // lanesAfter this commit's parents continue on
}

// Log returns recent history across local branches and the origin
// remote, in topological order, with lanes assigned.
func (r *Repo) Log(limit int) ([]*GraphRow, error) {
	out, err := r.gitRaw("log", "--topo-order", "--branches", "--remotes=origin", "--tags",
		"--max-count="+strconv.Itoa(limit),
		"--pretty=format:%H%x00%h%x00%P%x00%an%x00%ct%x00%D%x00%s")
	if err != nil {
		// A repo with no commits has no log.
		return nil, nil
	}
	head := r.HeadSHA()
	var commits []Commit
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.Split(line, "\x00")
		if len(parts) != 7 {
			continue
		}
		c := Commit{
			SHA:     parts[0],
			Short:   parts[1],
			Author:  parts[3],
			Subject: parts[6],
			IsHead:  parts[0] == head,
		}
		if parts[2] != "" {
			c.Parents = strings.Fields(parts[2])
		}
		if ts, err := strconv.ParseInt(parts[4], 10, 64); err == nil {
			c.When = time.Unix(ts, 0)
		}
		for _, ref := range strings.Split(parts[5], ",") {
			ref = strings.TrimSpace(ref)
			if ref == "" {
				continue
			}
			ref = strings.TrimPrefix(ref, "HEAD -> ")
			if ref == "HEAD" {
				continue
			}
			c.Refs = append(c.Refs, ref)
		}
		commits = append(commits, c)
	}
	return assignLanes(commits), nil
}

// assignLanes walks topo-ordered commits, tracking which commit each
// lane expects next.
func assignLanes(commits []Commit) []*GraphRow {
	var active []string // active[i] = SHA the lane expects
	rows := make([]*GraphRow, 0, len(commits))

	for _, c := range commits {
		row := &GraphRow{Commit: c}
		before := append([]string(nil), active...)

		// Which lanes were waiting for this commit?
		var matches []int
		for i, sha := range before {
			if sha == c.SHA {
				matches = append(matches, i)
			}
		}
		if len(matches) == 0 { // a tip: open a new lane
			row.IsTip = true
			before = append(before, c.SHA)
			matches = []int{len(before) - 1}
		}
		row.Dot = matches[0]
		for _, m := range matches[1:] {
			row.Merge = append(row.Merge, m)
		}

		// Build the after-state: matched lanes close; the first parent
		// takes the dot lane unless it is already tracked elsewhere;
		// extra parents join existing lanes or open new ones.
		type slot struct {
			sha string
			del bool
		}
		slots := make([]slot, len(before))
		for i, sha := range before {
			slots[i] = slot{sha: sha, del: sha == c.SHA}
		}
		find := func(sha string) int {
			for i, s := range slots {
				if !s.del && s.sha == sha {
					return i
				}
			}
			return -1
		}
		var forkSlots []int
		for pi, p := range c.Parents {
			if k := find(p); k >= 0 {
				forkSlots = append(forkSlots, k)
				continue
			}
			if pi == 0 {
				slots[row.Dot] = slot{sha: p}
				forkSlots = append(forkSlots, row.Dot)
			} else {
				slots = append(slots, slot{sha: p})
				forkSlots = append(forkSlots, len(slots)-1)
			}
		}

		// Compact deleted lanes, mapping old indices to new.
		newIdx := make([]int, len(slots))
		active = active[:0]
		for i, s := range slots {
			if s.del {
				newIdx[i] = -1
				continue
			}
			newIdx[i] = len(active)
			active = append(active, s.sha)
		}
		for i, sha := range before {
			if sha == c.SHA || i >= len(newIdx) || newIdx[i] < 0 {
				continue
			}
			row.Pass = append(row.Pass, [2]int{i, newIdx[i]})
		}
		for _, k := range forkSlots {
			if k < len(newIdx) && newIdx[k] >= 0 {
				row.Fork = append(row.Fork, newIdx[k])
			}
		}
		row.NLanes = max(len(before), len(active))
		rows = append(rows, row)
	}
	return rows
}
