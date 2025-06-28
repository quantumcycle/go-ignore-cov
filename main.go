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

func readIgnoreCoverageFromSourceDir(root string) ([]IgnoreCoverage, error) {
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

	return ignores, nil
}

func resolveFile(file string) (string, error) {
	dir, file := filepath.Split(file)
	pkg, err := build.Import(dir, ".", build.FindOnly)
	if err != nil {
		return "", err
	}
	return filepath.Join(pkg.Dir, file), nil
}

func findIgnoreCoveragesByFile(ignoreCoverages []IgnoreCoverage, file string) (*IgnoreCoverage, bool) {
	for _, ignore := range ignoreCoverages {
		if ignore.Filepath == file {
			return &ignore, true
		}
	}
	return nil, false
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
		},
		Action: func(c *cli.Context) error {

			verbose := c.Bool("verbose")

			root := c.String("root")
			if root == "" {
				root, _ = os.Getwd()
				if verbose {
					fmt.Printf("Module root not defined, using %s working directory as root\n", root)
				}
			}

			ignoreCoverages, err := readIgnoreCoverageFromSourceDir(root)
			if err != nil {
				return err
			}

			//scan code, find ignored lines
			coverageFile := c.String("file")
			profiles, err := cover.ParseProfiles(coverageFile)
			if err != nil {
				return err
			}

			for _, profile := range profiles {
				pgkPath := profile.FileName
				file, err := resolveFile(pgkPath)
				if err != nil {
					return err
				}

				if ignore, found := findIgnoreCoveragesByFile(ignoreCoverages, file); found {
					updateProfileFromIgnoreCoverages(profile, ignore, verbose)
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

			return nil
		},
	}

	err := app.Run(os.Args)
	if err != nil {
		log.Fatal(err)
	}
}
