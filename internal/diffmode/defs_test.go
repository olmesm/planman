package diffmode

import "testing"

func TestClassifyDefinition(t *testing.T) {
	cases := []struct {
		ident, path, line string
		wantKind          string // "" = no match
	}{
		{"Greet", "lib.go", "func Greet() string {", "function"},
		{"Greet", "lib.go", "func (s *Svc) Greet() string {", "function"},
		{"Config", "types.go", "type Config struct {", "declaration"},
		{"Greet", "lib.go", "\treturn Greet()", ""},
		{"handle", "app.ts", "export async function handle(req) {", "function"},
		{"Widget", "ui.tsx", "class Widget extends Component {", "class"},
		{"parse", "tool.py", "def parse(data):", "function"},
		{"parse", "tool.py", "    result = parse(data)", ""},
		{"MAX", "consts.rs", "pub const MAX: usize = 10;", "declaration"},
		{"run", "job.rb", "def self.run", "method"},
		{"main", "prog.c", "int main(int argc, char **argv) {", "function"},
		{"greet", "util.sh", "greet() {", "function"},
		{"x", "notes.txt", "x = 1", ""}, // unsupported extension
	}
	for _, tc := range cases {
		got := classifyDefinition(tc.ident, tc.path, tc.line)
		if tc.wantKind == "" {
			if got != nil {
				t.Errorf("%s in %s %q: expected no match, got %+v", tc.ident, tc.path, tc.line, got)
			}
			continue
		}
		if got == nil || got.kind != tc.wantKind {
			t.Errorf("%s in %s %q: want %s, got %+v", tc.ident, tc.path, tc.line, tc.wantKind, got)
		}
	}
}

func TestContainsIdentifierWordBoundaries(t *testing.T) {
	if !containsIdentifier("run", "func run() {") {
		t.Error("exact word should match")
	}
	if containsIdentifier("run", "func runner() {") {
		t.Error("prefix of a longer word must not match")
	}
	if containsIdentifier("run", "dryrun := true") {
		t.Error("suffix of a longer word must not match")
	}
	if !containsIdentifier("run", "x.runner = run") {
		t.Error("later standalone occurrence should match")
	}
}

func TestValidIdentifier(t *testing.T) {
	for _, ok := range []string{"Greet", "_private", "$el", "snake_case", "π"} {
		if !validIdentifier(ok) {
			t.Errorf("%q should be valid", ok)
		}
	}
	for _, bad := range []string{"", "9lives", "a-b", "func", "return", "with space"} {
		if validIdentifier(bad) {
			t.Errorf("%q should be rejected", bad)
		}
	}
}
