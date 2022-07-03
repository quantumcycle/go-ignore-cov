package main

import (
	"bufio"
	"fmt"
	"go/build"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/urfave/cli/v2"
	"golang.org/x/tools/cover"
)

const (
	InstructionBlock   = "block"
	InstructionFile    = "file"
	DefaultInstruction = InstructionBlock
)

type IgnoreCoverage struct {
	Filepath     string
	Instructions []Instruction
}

type Instruction interface {
	UpdateProfile(profile *cover.Profile, verbose bool)
}

type IgnoreBlock struct {
	LineNumber int
}

func (ig IgnoreBlock) UpdateProfile(profile *cover.Profile, verbose bool) {
	newBlocks := []cover.ProfileBlock{}
	for _, block := range profile.Blocks {
		if block.StartLine < ig.LineNumber && block.EndLine >= ig.LineNumber {
			//whole block inside the ignore zone, just ignore it
			if verbose {
				fmt.Printf("Removing coverage block [%d.%d] => [%d.%d] for %s\n",
					block.StartLine, block.StartCol, block.EndLine, block.EndCol, profile.FileName)
			}
			continue
		}
		newBlocks = append(newBlocks, block)
	}
	profile.Blocks = newBlocks
}

type IgnoreFile struct{}

func (ig IgnoreFile) UpdateProfile(profile *cover.Profile, verbose bool) {
	profile.Blocks = []cover.ProfileBlock{}
	if verbose {
		fmt.Printf("Removing all coverage blocks for %s\n", profile.FileName)
	}
}

func find(strs []string, str string) int {
	for i, s := range strs {
		if s == str {
			return i
		}
	}
	return -1
}

func getInstructionFromLine(line string) (string, bool) {
	if strings.Contains(line, "//coverage:ignore") || strings.Contains(line, "// coverage:ignore") {
		re := regexp.MustCompile(`//\s?coverage:ignore(\s([a-z]+))?$`)
		matches := re.FindStringSubmatch(line)
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
	for scanner.Scan() {
		lineTxt := scanner.Text()
		if instruction, ok := getInstructionFromLine(lineTxt); ok {
			if instruction == InstructionFile {
				instructions = append(instructions, IgnoreFile{})
			} else if instruction == InstructionBlock {
				instructions = append(instructions, IgnoreBlock{
					LineNumber: lineNumber + 1,
				})
			} else {
				return nil, fmt.Errorf("Unexpected ignore instruction [%s] at line %d in file [%s]", instruction, lineNumber, path)
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
	ignores := []IgnoreCoverage{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasSuffix(info.Name(), ".go") {
			instructions, err := readInstructionsFromSourceFile(path)
			if err != nil {
				return err
			}
			if len(instructions) > 0 {
				ignores = append(ignores, IgnoreCoverage{
					Filepath:     path,
					Instructions: instructions,
				})
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
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
		Version: "0.2.0",
		Usage:   "Remove ignored code from codebase from a golang coverage output file",
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
