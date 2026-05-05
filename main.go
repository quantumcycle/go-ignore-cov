//coverage:ignore file
package main

import (
	"bufio"
	"bytes"
	"fmt"
	"go/build"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/urfave/cli/v2"
	"golang.org/x/tools/cover"
	"gopkg.in/yaml.v3"
)

const (
	InstructionBlock   = "block"
	InstructionFile    = "file"
	DefaultInstruction = InstructionBlock
)

type ReasonConfig struct {
	Reasons []ReasonEntry `yaml:"reasons"`
}

type ReasonEntry struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

type IgnoreCoverage struct {
	Filepath     string
	Instructions []Instruction
}

type Instruction interface {
	UpdateProfile(profile *cover.Profile, verbose bool)
}

type IgnoreBlock struct {
	Line    int
	Col     int
	Reason  string
	SrcLine int
}

func (ig IgnoreBlock) UpdateProfile(profile *cover.Profile, verbose bool) {
	// Use arithmetic instead of string formatting for better performance
	//this is equivalent to igPos,_ := strconv.Atoi(fmt.Sprintf("%d%05d",ig.Line, ig.Col))
	igPos := ig.Line*100000 + ig.Col
	for i, block := range profile.Blocks {
		blockStart := block.StartLine*100000 + block.StartCol
		blockEnd := block.EndLine*100000 + block.EndCol
		if igPos >= blockStart && igPos < blockEnd {
			if block.Count == 0 {
				profile.Blocks[i].Count = 1
				if verbose {
					fmt.Printf("Setting coverage block [%d.%d] => [%d.%d] count to 1 for %s\n",
						block.StartLine, block.StartCol, block.EndLine, block.EndCol, profile.FileName)
				}
			}
		}
	}
}

type IgnoreFile struct {
	Reason  string
	SrcLine int
}

func (ig IgnoreFile) UpdateProfile(profile *cover.Profile, verbose bool) {
	for i := range profile.Blocks {
		if profile.Blocks[i].Count == 0 {
			profile.Blocks[i].Count = 1
		}
	}
	if verbose {
		fmt.Printf("Setting coverage blocks [all] count to 1 for %s\n", profile.FileName)
	}
}

type PatternIgnore struct {
	MatchedBy string
}

func (pi PatternIgnore) UpdateProfile(profile *cover.Profile, verbose bool) {
	for i := range profile.Blocks {
		if profile.Blocks[i].Count == 0 {
			profile.Blocks[i].Count = 1
		}
	}
	if verbose {
		fmt.Printf("Setting coverage blocks [all] count to 1 for %s (matched by %s)\n",
			profile.FileName, pi.MatchedBy)
	}
}

// getInstructionFromLine parses a coverage:ignore directive from a source line.
// Returns instruction ("block" or "file"), optional reason, and whether a directive was found.
func getInstructionFromLine(line string) (instruction string, reason string, ok bool) {
	if !strings.Contains(line, "coverage:ignore") {
		return "", "", false
	}
	idx := strings.Index(line, "coverage:ignore")
	// Verify preceded by // with optional whitespace
	prefix := strings.TrimRight(line[:idx], " \t")
	if !strings.HasSuffix(prefix, "//") {
		return "", "", false
	}
	tail := strings.TrimSpace(line[idx+len("coverage:ignore"):])
	tokens := strings.Fields(tail)
	instruction = InstructionBlock
	for _, tok := range tokens {
		switch {
		case tok == "file":
			instruction = InstructionFile
		case strings.HasPrefix(tok, "reason="):
			reason = strings.TrimPrefix(tok, "reason=")
		}
	}
	return instruction, reason, true
}

func readInstructionsFromSourceFile(path string) ([]Instruction, error) {
	instructions := []Instruction{}
	source, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer source.Close()
	scanner := bufio.NewScanner(source)
	lineNumber := 1
	pendingBlock := false
	var pendingReason string
	var pendingSrcLine int
	for scanner.Scan() {
		lineTxt := scanner.Text()
		if instruction, reason, ok := getInstructionFromLine(lineTxt); ok {
			if instruction == InstructionFile {
				instructions = append(instructions, IgnoreFile{Reason: reason, SrcLine: lineNumber})
			} else if instruction == InstructionBlock {
				pendingBlock = true
				pendingReason = reason
				pendingSrcLine = lineNumber
			} else {
				return nil, fmt.Errorf("Unexpected ignore instruction [%s] at line %d in file [%s]", instruction, lineNumber, path)
			}
		} else {
			if pendingBlock {
				colStart := len(lineTxt) - len(strings.TrimLeft(lineTxt, "\t ")) + 1
				instructions = append(instructions, IgnoreBlock{
					Line:    lineNumber,
					Col:     colStart,
					Reason:  pendingReason,
					SrcLine: pendingSrcLine,
				})
				pendingBlock = false
				pendingReason = ""
				pendingSrcLine = 0
			}
		}
		lineNumber++
	}

	if err := scanner.Err(); err != nil {
		return []Instruction{}, err
	}

	return instructions, nil
}

func readIgnoreCoverageFromSourceDir(root string, verbose bool) ([]IgnoreCoverage, error) {
	walkStart := time.Now()
	var goFiles []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".go") {
			goFiles = append(goFiles, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	walkDuration := time.Since(walkStart)

	if verbose {
		fmt.Printf("File tree walk completed in %v, found %d .go files\n", walkDuration, len(goFiles))
	}

	processStart := time.Now()

	numWorkers := runtime.NumCPU()
	jobs := make(chan string, len(goFiles))
	results := make(chan IgnoreCoverage, len(goFiles))
	var wg sync.WaitGroup

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for path := range jobs {
				content, err := os.ReadFile(path)
				if err != nil {
					continue
				}
				if !bytes.Contains(content, []byte("coverage:ignore")) {
					continue
				}

				instructions, err := readInstructionsFromSourceFile(path)
				if err != nil {
					continue
				}
				if len(instructions) > 0 {
					results <- IgnoreCoverage{
						Filepath:     path,
						Instructions: instructions,
					}
				}
			}
		}()
	}

	for _, file := range goFiles {
		jobs <- file
	}
	close(jobs)

	go func() {
		wg.Wait()
		close(results)
	}()

	var ignores []IgnoreCoverage
	for ignore := range results {
		ignores = append(ignores, ignore)
	}

	processDuration := time.Since(processStart)
	if verbose {
		fmt.Printf("Source file processing completed in %v, found %d files with ignore comments\n",
			processDuration, len(ignores))
	}

	return ignores, nil
}

func buildPackagePathCache(profiles []*cover.Profile, verbose bool) (map[string]string, error) {
	cacheStart := time.Now()
	packageCache := make(map[string]string)

	uniqueDirs := make(map[string]bool)
	for _, profile := range profiles {
		dir, _ := filepath.Split(profile.FileName)
		uniqueDirs[dir] = true
	}

	for dir := range uniqueDirs {
		pkg, err := build.Import(dir, ".", build.FindOnly)
		if err != nil {
			if verbose {
				fmt.Printf("Warning: Could not resolve package %s, using original path\n", dir)
			}
			packageCache[dir] = dir
		} else {
			packageCache[dir] = pkg.Dir
		}
	}

	if verbose {
		fmt.Printf("Package cache built in %v for %d unique directories\n",
			time.Since(cacheStart), len(packageCache))
	}

	return packageCache, nil
}

func resolveFileWithCache(packagePath string, packageCache map[string]string) string {
	dir, file := filepath.Split(packagePath)
	if pkgDir, found := packageCache[dir]; found {
		return filepath.Join(pkgDir, file)
	}
	return packagePath
}

func buildIgnoreCoverageMap(ignoreCoverages []IgnoreCoverage) map[string]*IgnoreCoverage {
	ignoreMap := make(map[string]*IgnoreCoverage, len(ignoreCoverages))
	for i := range ignoreCoverages {
		ignoreMap[ignoreCoverages[i].Filepath] = &ignoreCoverages[i]
	}
	return ignoreMap
}

type PatternMatcher struct {
	GlobPatterns  []string
	RegexPatterns []*regexp.Regexp
	Root          string
}

func buildPatternMatcher(globPatterns, regexPatterns, root string) (*PatternMatcher, error) {
	matcher := &PatternMatcher{Root: root}

	if globPatterns != "" {
		for _, pattern := range strings.Split(globPatterns, ",") {
			pattern = strings.TrimSpace(pattern)
			if pattern != "" {
				matcher.GlobPatterns = append(matcher.GlobPatterns, pattern)
			}
		}
	}

	if regexPatterns != "" {
		for _, pattern := range strings.Split(regexPatterns, ",") {
			pattern = strings.TrimSpace(pattern)
			if pattern != "" {
				regex, err := regexp.Compile(pattern)
				if err != nil {
					return nil, fmt.Errorf("invalid regex pattern '%s': %v", pattern, err)
				}
				matcher.RegexPatterns = append(matcher.RegexPatterns, regex)
			}
		}
	}

	return matcher, nil
}

func (pm *PatternMatcher) MatchesFile(absolutePath string) (string, bool) {
	relPath, err := filepath.Rel(pm.Root, absolutePath)
	if err != nil {
		relPath = absolutePath
	}

	for _, pattern := range pm.GlobPatterns {
		matched, err := doublestar.Match(pattern, relPath)
		if err == nil && matched {
			return fmt.Sprintf("glob pattern '%s'", pattern), true
		}

		matched, err = doublestar.Match(pattern, absolutePath)
		if err == nil && matched {
			return fmt.Sprintf("glob pattern '%s'", pattern), true
		}
	}

	for _, regex := range pm.RegexPatterns {
		if regex.MatchString(absolutePath) {
			return fmt.Sprintf("regex pattern '%s'", regex.String()), true
		}
		if regex.MatchString(relPath) {
			return fmt.Sprintf("regex pattern '%s'", regex.String()), true
		}
	}

	return "", false
}

func updateProfileFromIgnoreCoverages(profile *cover.Profile, ignore *IgnoreCoverage, verbose bool) {
	for _, instruction := range ignore.Instructions {
		instruction.UpdateProfile(profile, verbose)
	}
}

func writeProfiles(profiles []*cover.Profile, w io.Writer) {
	w.Write([]byte(fmt.Sprintf("mode: %s\n", profiles[0].Mode)))
	for _, profile := range profiles {
		for _, block := range profile.Blocks {
			w.Write([]byte(fmt.Sprintf("%s:%d.%d,%d.%d %d %d\n",
				profile.FileName,
				block.StartLine, block.StartCol,
				block.EndLine, block.EndCol,
				block.NumStmt, block.Count)))
		}
	}
}

type reasonViolation struct {
	filepath string
	line     int
	message  string
}

func validateReasons(ignoreCoverages []IgnoreCoverage, validReasons map[string]bool, reasonNames []string, requireReason bool) []reasonViolation {
	var violations []reasonViolation
	for _, ic := range ignoreCoverages {
		for _, instr := range ic.Instructions {
			var reason string
			var srcLine int
			switch v := instr.(type) {
			case IgnoreBlock:
				reason = v.Reason
				srcLine = v.SrcLine
			case IgnoreFile:
				reason = v.Reason
				srcLine = v.SrcLine
			default:
				continue
			}
			if requireReason && reason == "" {
				violations = append(violations, reasonViolation{ic.Filepath, srcLine, "coverage:ignore missing required reason tag"})
			} else if reason != "" && !validReasons[reason] {
				msg := fmt.Sprintf("coverage:ignore has unknown reason %q (valid: %s)", reason, strings.Join(reasonNames, ", "))
				violations = append(violations, reasonViolation{ic.Filepath, srcLine, msg})
			}
		}
	}
	return violations
}

func main() {
	cli.VersionFlag = &cli.BoolFlag{
		Name:    "print-version",
		Aliases: []string{"V"},
		Usage:   "print only the version",
	}

	app := &cli.App{
		Name:    "go-ignore-cov",
		Version: "0.6.1",
		Usage:   "Mark ignored code as covered in a golang coverage output file",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     "file",
				Aliases:  []string{"f"},
				Usage:    "input coverage file",
				Required: true,
			},
			&cli.StringFlag{
				Name:    "output",
				Aliases: []string{"o"},
				Usage:   "output coverage file",
			},
			&cli.StringFlag{
				Name:    "root",
				Aliases: []string{"r"},
				Usage:   "module root",
			},
			&cli.BoolFlag{
				Name:    "verbose",
				Aliases: []string{"v"},
				Usage:   "verbose output",
			},
			&cli.BoolFlag{
				Name:    "ignore-empty",
				Aliases: []string{"e"},
				Usage:   "ignore empty functions (functions with 0 statements)",
			},
			&cli.StringFlag{
				Name:    "exclude-globs",
				Aliases: []string{"g"},
				Usage:   "comma-separated glob patterns to exclude (e.g., \"**/test/**,**/*_gen.go\")",
			},
			&cli.StringFlag{
				Name:    "exclude-regex",
				Aliases: []string{"x"},
				Usage:   "comma-separated regex patterns to exclude (e.g., \"/test/,.*_gen\\.go$\")",
			},
			&cli.StringFlag{
				Name:    "config",
				Aliases: []string{"c"},
				Usage:   "path to reasons config YAML file (default: .coverage-reasons.yml in root)",
			},
			&cli.BoolFlag{
				Name:  "require-reason",
				Usage: "fail if any //coverage:ignore directive is missing a reason tag",
			},
		},
		Action: func(c *cli.Context) error {

			start := time.Now()
			verbose := c.Bool("verbose")

			root := c.String("root")
			if root == "" {
				root, _ = os.Getwd()
				if verbose {
					fmt.Printf("Module root not defined, using %s working directory as root\n", root)
				}
			}
			root, _ = filepath.Abs(root)

			globPatterns := c.String("exclude-globs")
			regexPatterns := c.String("exclude-regex")

			var patternMatcher *PatternMatcher
			if globPatterns != "" || regexPatterns != "" {
				var err error
				patternMatcher, err = buildPatternMatcher(globPatterns, regexPatterns, root)
				if err != nil {
					return fmt.Errorf("error building pattern matcher: %v", err)
				}
				if verbose {
					fmt.Printf("Pattern matcher configured with %d glob patterns and %d regex patterns\n",
						len(patternMatcher.GlobPatterns), len(patternMatcher.RegexPatterns))
				}
			}

			ignoreCoverages, err := readIgnoreCoverageFromSourceDir(root, verbose)
			if err != nil {
				return err
			}

			// Reason validation
			configFlag := c.String("config")
			requireReason := c.Bool("require-reason")
			configPath := configFlag
			if configPath == "" {
				configPath = filepath.Join(root, ".coverage-reasons.yml")
			}

			var validReasons map[string]bool
			var reasonNames []string
			configLoaded := false
			if _, statErr := os.Stat(configPath); statErr == nil {
				data, err := os.ReadFile(configPath)
				if err != nil {
					return fmt.Errorf("reading config: %v", err)
				}
				var cfg ReasonConfig
				if err := yaml.Unmarshal(data, &cfg); err != nil {
					return fmt.Errorf("parsing config: %v", err)
				}
				validReasons = make(map[string]bool, len(cfg.Reasons))
				for _, r := range cfg.Reasons {
					validReasons[r.Name] = true
					reasonNames = append(reasonNames, r.Name)
				}
				configLoaded = true
			} else if configFlag != "" {
				return fmt.Errorf("config file not found: %s", configPath)
			}

			if requireReason && !configLoaded {
				return fmt.Errorf("--require-reason requires a config file (use --config or create .coverage-reasons.yml)")
			}

			if configLoaded {
				violations := validateReasons(ignoreCoverages, validReasons, reasonNames, requireReason)
				if len(violations) > 0 {
					for _, v := range violations {
						fmt.Fprintf(os.Stderr, "%s:%d: %s\n", v.filepath, v.line, v.message)
					}
					return cli.Exit("", 1)
				}
			}

			coverageFile := c.String("file")
			profiles, err := cover.ParseProfiles(coverageFile)
			if err != nil {
				return err
			}

			packageCache, err := buildPackagePathCache(profiles, verbose)
			if err != nil {
				return err
			}

			ignoreMap := buildIgnoreCoverageMap(ignoreCoverages)

			exclusionStart := time.Now()
			commentExclusions := 0
			patternExclusions := 0

			for _, profile := range profiles {
				file := resolveFileWithCache(profile.FileName, packageCache)

				if patternMatcher != nil {
					if matchedBy, matches := patternMatcher.MatchesFile(file); matches {
						patternIgnore := &IgnoreCoverage{
							Filepath:     file,
							Instructions: []Instruction{PatternIgnore{MatchedBy: matchedBy}},
						}
						updateProfileFromIgnoreCoverages(profile, patternIgnore, verbose)
						patternExclusions++
						continue
					}
				}

				if ignore, found := ignoreMap[file]; found {
					updateProfileFromIgnoreCoverages(profile, ignore, verbose)
					commentExclusions++
				}
			}

			exclusionDuration := time.Since(exclusionStart)

			ignoreEmpty := c.Bool("ignore-empty")
			emptyFunctions := 0
			if ignoreEmpty {
				emptyStart := time.Now()
				for _, profile := range profiles {
					for i, block := range profile.Blocks {
						if block.NumStmt == 0 {
							profile.Blocks[i].NumStmt = 1
							profile.Blocks[i].Count = 1
							emptyFunctions++
							if verbose {
								fmt.Printf("Setting empty function to covered for %s:%d.%d\n",
									profile.FileName, block.StartLine, block.StartCol)
							}
						}
					}
				}
				if verbose {
					fmt.Printf("Empty function processing completed in %v, processed %d empty functions\n",
						time.Since(emptyStart), emptyFunctions)
				}
			}

			if verbose {
				fmt.Printf("Coverage exclusion processing completed in %v\n", exclusionDuration)
				fmt.Printf("  - %d profiles excluded by comments\n", commentExclusions)
				fmt.Printf("  - %d profiles excluded by patterns\n", patternExclusions)
				if ignoreEmpty {
					fmt.Printf("  - %d empty functions marked as covered\n", emptyFunctions)
				}
			}

			output := c.String("output")
			if output == "" {
				output = coverageFile
			}
			outputFile, err := os.Create(output)
			if err != nil {
				return err
			}
			defer outputFile.Close()

			if verbose {
				fmt.Printf("Writing updated coverage to %s ... \n", output)
			}

			writeProfiles(profiles, outputFile)

			if verbose {
				fmt.Printf("Finished in %v\n", time.Since(start))
			}
			return nil
		},
	}

	err := app.Run(os.Args)
	if err != nil {
		log.Fatal(err)
	}
}
