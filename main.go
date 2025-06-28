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
)

const (
	InstructionBlock   = "block"
	InstructionFile    = "file"
	DefaultInstruction = InstructionBlock
)

var (
	// Compile regex once at package level for performance
	coverageIgnoreRegex = regexp.MustCompile(`//\s?coverage:ignore(\s([a-z]+))?$`)
)

type IgnoreCoverage struct {
	Filepath     string
	Instructions []Instruction
}

type Instruction interface {
	UpdateProfile(profile *cover.Profile, verbose bool)
}

type IgnoreBlock struct {
	Line int
	Col  int
}

func (ig IgnoreBlock) UpdateProfile(profile *cover.Profile, verbose bool) {
	// Use arithmetic instead of string formatting for better performance
	//this is equivalent to igPos,_ := strconv.Atoi(fmt.Sprintf("%d%05d",ig.Line, ig.Col))
	igPos := ig.Line*100000 + ig.Col
	for i, block := range profile.Blocks {
		blockStart := block.StartLine*100000 + block.StartCol
		blockEnd := block.EndLine*100000 + block.EndCol
		if igPos >= blockStart && igPos < blockEnd {
			//whole block inside the ignore zone, set count to at least 1 to simulate coverage
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

type IgnoreFile struct{}

func (ig IgnoreFile) UpdateProfile(profile *cover.Profile, verbose bool) {
	//all blocks in that file, set count to at least 1 to simulate coverage
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
	//all blocks in that file, set count to at least 1 to simulate coverage
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

func getInstructionFromLine(line string) (string, bool) {
	if strings.Contains(line, "//coverage:ignore") || strings.Contains(line, "// coverage:ignore") {
		matches := coverageIgnoreRegex.FindStringSubmatch(line)
		if len(matches) == 3 {
			if matches[2] != "" {
				return matches[2], true
			}
			return DefaultInstruction, true
		}
	}
	return "", false
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
	pendingBlockInstruction := ""
	for scanner.Scan() {
		lineTxt := scanner.Text()
		if instruction, ok := getInstructionFromLine(lineTxt); ok {
			if instruction == InstructionFile {
				instructions = append(instructions, IgnoreFile{})
			} else if instruction == InstructionBlock {
				pendingBlockInstruction = instruction
			} else {
				return nil, fmt.Errorf("Unexpected ignore instruction [%s] at line %d in file [%s]", instruction, lineNumber, path)
			}
		} else {
			if pendingBlockInstruction != "" {
				colStart := len(lineTxt) - len(strings.TrimLeft(lineTxt, "\t ")) + 1
				instructions = append(instructions, IgnoreBlock{
					Line: lineNumber,
					Col:  colStart,
				})
				pendingBlockInstruction = ""
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
	// Time the file tree walking
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

	// Time the source file processing
	processStart := time.Now()

	// Process files in parallel
	numWorkers := runtime.NumCPU()
	jobs := make(chan string, len(goFiles))
	results := make(chan IgnoreCoverage, len(goFiles))
	var wg sync.WaitGroup

	// Start workers
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for path := range jobs {
				// Quick check if file contains coverage:ignore before full parsing
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

	// Send jobs to waiting workers
	for _, file := range goFiles {
		jobs <- file
	}
	close(jobs)

	// Wait for workers to finish
	go func() {
		wg.Wait()
		close(results)
	}()

	// Collect results
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

	// Get unique package directories
	uniqueDirs := make(map[string]bool)
	for _, profile := range profiles {
		dir, _ := filepath.Split(profile.FileName)
		uniqueDirs[dir] = true
	}

	// Resolve each unique directory once
	for dir := range uniqueDirs {
		pkg, err := build.Import(dir, ".", build.FindOnly)
		if err != nil {
			if verbose {
				fmt.Printf("Warning: Could not resolve package %s, using original path\n", dir)
			}
			// Fallback: use the original directory path
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
	// Fallback (shouldn't happen if cache is complete)
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

	// Parse glob patterns
	if globPatterns != "" {
		for _, pattern := range strings.Split(globPatterns, ",") {
			pattern = strings.TrimSpace(pattern)
			if pattern != "" {
				matcher.GlobPatterns = append(matcher.GlobPatterns, pattern)
			}
		}
	}

	// Parse and compile regex patterns
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
	// Get relative path from root for glob matching
	relPath, err := filepath.Rel(pm.Root, absolutePath)
	if err != nil {
		relPath = absolutePath
	}

	// Check glob patterns using doublestar library
	for _, pattern := range pm.GlobPatterns {
		// Try matching against relative path
		matched, err := doublestar.Match(pattern, relPath)
		if err == nil && matched {
			return fmt.Sprintf("glob pattern '%s'", pattern), true
		}

		// Also try matching against absolute path for some patterns
		matched, err = doublestar.Match(pattern, absolutePath)
		if err == nil && matched {
			return fmt.Sprintf("glob pattern '%s'", pattern), true
		}
	}

	// Check regex patterns
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

func main() {
	cli.VersionFlag = &cli.BoolFlag{
		Name:    "print-version",
		Aliases: []string{"V"},
		Usage:   "print only the version",
	}

	app := &cli.App{
		Name:    "go-ignore-cov",
		Version: "0.5.0",
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

			// Build pattern matcher from CLI flags
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

			//scan code, find ignored lines
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
				// Use cached package resolution - no expensive build.Import calls
				file := resolveFileWithCache(profile.FileName, packageCache)

				// First check pattern-based ignores (they take precedence over comments)
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

				// Then check comment-based ignores (fallback if no pattern matches)
				if ignore, found := ignoreMap[file]; found {
					updateProfileFromIgnoreCoverages(profile, ignore, verbose)
					commentExclusions++
				}
			}

			exclusionDuration := time.Since(exclusionStart)
			if verbose {
				fmt.Printf("Coverage exclusion processing completed in %v\n", exclusionDuration)
				fmt.Printf("  - %d profiles excluded by comments\n", commentExclusions)
				fmt.Printf("  - %d profiles excluded by patterns\n", patternExclusions)
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

			fmt.Printf("Finished in %v\n", time.Since(start))
			return nil
		},
	}

	err := app.Run(os.Args)
	if err != nil {
		log.Fatal(err)
	}
}
