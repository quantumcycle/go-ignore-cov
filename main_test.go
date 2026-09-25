package main

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/tools/cover"
)

func TestGetInstructionFromLine(t *testing.T) {
	tests := []struct {
		line        string
		instruction string
		reason      string
		ok          bool
	}{
		{"//coverage:ignore", "block", "", true},
		{"// coverage:ignore", "block", "", true},
		{"//coverage:ignore file", "file", "", true},
		{"// coverage:ignore file", "file", "", true},
		{"//coverage:ignore reason=unreachable", "block", "unreachable", true},
		{"// coverage:ignore reason=unreachable", "block", "unreachable", true},
		{"//coverage:ignore file reason=generated", "file", "generated", true},
		{"// coverage:ignore file reason=generated", "file", "generated", true},
		{"x := foo() //coverage:ignore reason=impossible-error", "block", "impossible-error", true},
		{"// some other comment", "", "", false},
		{"", "", "", false},
		{`s := "coverage:ignore"`, "", "", false},
		// // embedded in a string literal must not be treated as a directive.
		{"var re = regexp.MustCompile(`//coverage:ignore`)", "", "", false},
		{`var re = regexp.MustCompile(` + "`" + `\S.*//coverage:ignore` + "`" + `)`, "", "", false},
		{`msg := "//coverage:ignore must have reason"`, "", "", false},
		{`msg := fmt.Sprintf("//coverage:ignore has unknown reason=%q", r)`, "", "", false},
	}
	for _, tt := range tests {
		instruction, reason, ok := getInstructionFromLine(tt.line)
		if ok != tt.ok || instruction != tt.instruction || reason != tt.reason {
			t.Errorf("getInstructionFromLine(%q) = (%q, %q, %v), want (%q, %q, %v)",
				tt.line, instruction, reason, ok, tt.instruction, tt.reason, tt.ok)
		}
	}
}

func TestReadInstructionsFromSourceFile(t *testing.T) {
	src := `package foo

//coverage:ignore reason=unreachable
func unreachable() {
	panic("unreachable")
}

//coverage:ignore file reason=generated
`
	f, err := os.CreateTemp("", "*.go")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	f.WriteString(src)
	f.Close()

	instrs, err := readInstructionsFromSourceFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	if len(instrs) != 2 {
		t.Fatalf("expected 2 instructions, got %d", len(instrs))
	}

	block, ok := instrs[0].(IgnoreBlock)
	if !ok {
		t.Fatalf("expected IgnoreBlock, got %T", instrs[0])
	}
	if block.Reason != "unreachable" {
		t.Errorf("expected reason=unreachable, got %q", block.Reason)
	}
	if block.SrcLine != 3 {
		t.Errorf("expected SrcLine=3, got %d", block.SrcLine)
	}
	if block.Line != 4 {
		t.Errorf("expected Line=4, got %d", block.Line)
	}

	file, ok := instrs[1].(IgnoreFile)
	if !ok {
		t.Fatalf("expected IgnoreFile, got %T", instrs[1])
	}
	if file.Reason != "generated" {
		t.Errorf("expected reason=generated, got %q", file.Reason)
	}
	if file.SrcLine != 8 {
		t.Errorf("expected SrcLine=8, got %d", file.SrcLine)
	}
}

func TestReadInstructionsNoReason(t *testing.T) {
	src := `package foo

//coverage:ignore
func foo() {
	panic("unreachable")
}
`
	f, err := os.CreateTemp("", "*.go")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	f.WriteString(src)
	f.Close()

	instrs, err := readInstructionsFromSourceFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	if len(instrs) != 1 {
		t.Fatalf("expected 1 instruction, got %d", len(instrs))
	}
	block, ok := instrs[0].(IgnoreBlock)
	if !ok {
		t.Fatalf("expected IgnoreBlock, got %T", instrs[0])
	}
	if block.Reason != "" {
		t.Errorf("expected empty reason, got %q", block.Reason)
	}
}

func TestValidateReasons(t *testing.T) {
	valid := map[string]bool{"unreachable": true, "generated": true}
	names := []string{"unreachable", "generated"}

	ic := []IgnoreCoverage{
		{
			Filepath: "foo.go",
			Instructions: []Instruction{
				IgnoreBlock{Line: 5, Col: 1, Reason: "unknown-reason", SrcLine: 4},
			},
		},
	}

	violations := validateReasons(ic, valid, names, false)
	if len(violations) != 1 {
		t.Fatalf("expected 1 violation, got %d", len(violations))
	}
	if violations[0].filepath != "foo.go" || violations[0].line != 4 {
		t.Errorf("unexpected violation location: %+v", violations[0])
	}
}

func TestValidateReasonsRequireReason(t *testing.T) {
	valid := map[string]bool{"unreachable": true}
	names := []string{"unreachable"}

	ic := []IgnoreCoverage{
		{
			Filepath: "bar.go",
			Instructions: []Instruction{
				IgnoreBlock{Line: 10, Col: 1, Reason: "", SrcLine: 9},
				IgnoreBlock{Line: 20, Col: 1, Reason: "unreachable", SrcLine: 19},
			},
		},
	}

	violations := validateReasons(ic, valid, names, true)
	if len(violations) != 1 {
		t.Fatalf("expected 1 violation (missing reason), got %d", len(violations))
	}
	if violations[0].line != 9 {
		t.Errorf("expected violation at line 9, got %d", violations[0].line)
	}
}

func TestFilterIgnoreCoveragesExcludesMatchedPaths(t *testing.T) {
	pm := &PatternMatcher{
		GlobPatterns: []string{"tools/**"},
		Root:         "/repo",
	}
	ics := []IgnoreCoverage{
		{Filepath: "/repo/tools/qualitygate/foo.go"},
		{Filepath: "/repo/service/domain/bar.go"},
	}
	got := filterIgnoreCoverages(ics, pm)
	if len(got) != 1 {
		t.Fatalf("expected 1 after filtering, got %d", len(got))
	}
	if got[0].Filepath != "/repo/service/domain/bar.go" {
		t.Errorf("unexpected filepath: %s", got[0].Filepath)
	}
}

func TestFilterIgnoreCoveragesNilMatcher(t *testing.T) {
	ics := []IgnoreCoverage{
		{Filepath: "/repo/tools/foo.go"},
		{Filepath: "/repo/service/bar.go"},
	}
	got := filterIgnoreCoverages(ics, nil)
	if len(got) != 2 {
		t.Fatalf("expected 2 (nil matcher keeps all), got %d", len(got))
	}
}

func TestValidateReasonsNoRequireNoConfig(t *testing.T) {
	// Without require-reason and no config, no validation happens at the call site
	// (config is nil). If called with nil validReasons, we need to guard.
	// In practice this function is only called when configLoaded=true, but let's
	// verify that a reason="" with requireReason=false produces no violations.
	valid := map[string]bool{"unreachable": true}
	names := []string{"unreachable"}

	ic := []IgnoreCoverage{
		{
			Filepath: "baz.go",
			Instructions: []Instruction{
				IgnoreBlock{Line: 5, Col: 1, Reason: "", SrcLine: 4},
			},
		},
	}

	violations := validateReasons(ic, valid, names, false)
	if len(violations) != 0 {
		t.Errorf("expected 0 violations, got %d", len(violations))
	}
}

func TestMainModuleDir(t *testing.T) {
	root := t.TempDir()
	tests := []struct {
		dir    string
		want   string
		mapped bool
	}{
		{"example.com/m/pkg/a/", filepath.Join(root, "pkg", "a"), true},
		{"example.com/m/", root, true},
		{"example.com/m", root, true},
		{"example.com/mother/pkg/", "", false},
		{"github.com/other/dep/", "", false},
	}
	for _, tt := range tests {
		got, mapped := mainModuleDir(tt.dir, "example.com/m", root)
		if mapped != tt.mapped || got != tt.want {
			t.Errorf("mainModuleDir(%q) = %q, %v; want %q, %v", tt.dir, got, mapped, tt.want, tt.mapped)
		}
	}
	if got, mapped := mainModuleDir("example.com/m/pkg/", "", root); mapped || got != "" {
		t.Errorf("without a module path nothing should map, got %q, %v", got, mapped)
	}
}

func TestMainModulePath(t *testing.T) {
	root := t.TempDir()
	if got := mainModulePath(root); got != "" {
		t.Errorf("no go.mod: want empty module path, got %q", got)
	}
	cases := map[string]string{
		"comment line above":      "// a comment\n\nmodule example.com/m\n\ngo 1.18\n",
		"trailing comment":        "module example.com/m // the module\n",
		"tab separator":           "module\texample.com/m\n",
		"quoted path":             "module \"example.com/m\"\n",
		"crlf":                    "module example.com/m\r\ngo 1.18\r\n",
		"unknown directive first": "toolchain go1.22.0\nmodule example.com/m\n",
	}
	for name, content := range cases {
		if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := mainModulePath(root); got != "example.com/m" {
			t.Errorf("%s: want example.com/m, got %q", name, got)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("go 1.18\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := mainModulePath(root); got != "" {
		t.Errorf("go.mod without module line: want empty module path, got %q", got)
	}
}

func TestBuildPackagePathCacheMapsMainModuleWithoutLookup(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/m\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	profiles := []*cover.Profile{
		{FileName: "example.com/m/pkg/a/file.go"},
		{FileName: "example.com/m/pkg/a/other.go"},
		{FileName: "example.com/m/main.go"},
	}
	cache, err := buildPackagePathCache(profiles, root, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := cache["example.com/m/pkg/a/"]; got != filepath.Join(root, "pkg", "a") {
		t.Errorf("pkg/a mapped to %q", got)
	}
	if got := cache["example.com/m/"]; got != root {
		t.Errorf("module root mapped to %q", got)
	}
	if got := resolveFileWithCache("example.com/m/pkg/a/file.go", cache); got != filepath.Join(root, "pkg", "a", "file.go") {
		t.Errorf("file resolved to %q", got)
	}
}
