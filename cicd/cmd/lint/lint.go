package lint

import (
	"bufio"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/goccy/go-yaml"
	"github.com/spf13/cobra"
)

const lintConfig = "cicd/cmd/lint/lint-config.yaml"

var LintCmd = &cobra.Command{
	Use:   "lint",
	Short: "A command to handle all lint functionalities",
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true
		err := lint()
		if err != nil {
			return err
		}
		return nil
	},
}

type LintConfig struct {
	Linters struct {
		Exclusions struct {
			Paths []string `yaml:"paths"`
		} `yaml:"exclusions"`
	} `yaml:"linters"`
}

func readLintConfig(configPath string) (LintConfig, error) {
	var config LintConfig
	data, err := ioutil.ReadFile(configPath)
	if err != nil {
		return config, err
	}
	err = yaml.Unmarshal(data, &config)
	return config, err
}

func checkGoFormat() int {
	exitCode := 0

	// Read lint-config.yaml
	config, err := readLintConfig(lintConfig)
	if err != nil {
		log.Printf("Error reading lint config: %v", err)
		return 1
	}

	// linters.exclusions.paths are regexps matched against the file path
	// (same semantics golangci-lint applies); compile them once.
	skipPatterns := make([]*regexp.Regexp, 0, len(config.Linters.Exclusions.Paths))
	for _, pattern := range config.Linters.Exclusions.Paths {
		re, err := regexp.Compile(pattern)
		if err != nil {
			log.Printf("Error compiling exclusion pattern %s: %v", pattern, err)
			return 1
		}
		skipPatterns = append(skipPatterns, re)
	}

	err = filepath.Walk(".", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip excluded paths; prune excluded directories entirely.
		for _, pattern := range skipPatterns {
			if pattern.MatchString(path) {
				if info.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}

		// Skip monkey mock test files
		if strings.HasSuffix(path, "monkey_mocks_test.go") {
			return nil
		}

		if strings.HasSuffix(path, ".go") && !strings.Contains(path, "go_path") {
			file, err := os.Open(path)
			if err != nil {
				log.Printf("Error opening file %s: %v", path, err)
				exitCode = 1
				return nil
			}
			defer file.Close()

			scanner := bufio.NewScanner(file)
			content := ""
			for scanner.Scan() {
				content += scanner.Text() + "\n"
			}

			// Skip checkGoFormat() function in lint.go as this has all the regex that is checked, caught in lint error.
			if strings.HasSuffix(path, "lint.go") && strings.Contains(content, "func checkGoFormat()") {
				return nil
			}

			// Check for multiple import groups
			importGroupRegex := regexp.MustCompile(`(?s)import \(\n(([^\)\n]+\n)+\n){2,}`)
			if importGroupRegex.MatchString(content) {
				log.Printf("%s has multiple groups inside import block", path)
				exitCode = 8
			}

			// Check for extraneous empty lines after opening/closing curly braces
			emptyLineRegex := regexp.MustCompile(`(?s)(.{60}\{\n\n|.{60}\n\n\t*\})`)
			if emptyLineRegex.MatchString(content) {
				log.Printf("%s has extraneous empty line after opening curly and/or before closing curly", path)
				exitCode = 9
			}

			// Check for comments missing space after "//"
			commentRegex := regexp.MustCompile(`(^//\S|\s//\S)`)
			if commentRegex.MatchString(content) && !strings.Contains(content, "//go:") {
				log.Printf("%s has comment(s) where space is missing after //", path)
				exitCode = 10
			}

			// Check for multiline comments
			if strings.Contains(content, "/*") && strings.Contains(content, "*/") {
				openIndex := strings.Index(content, "/*")
				closeIndex := strings.Index(content, "*/")
				if openIndex < closeIndex {
					log.Printf("%s has /* */ style comment(s)", path)
					exitCode = 11
				}
			}

			// Check for Println statements
			if strings.Contains(content, "fmt.Println") {
				log.Printf("%s has Println statement", path)
				exitCode = 14
			}

			// Check for printf statements
			if strings.Contains(content, "fmt.Printf") {
				log.Printf("%s has Printf statement", path)
				exitCode = 15
			}

			// Check for Pretty calls outside of functions
			if strings.Contains(content, "Pretty(") && !strings.Contains(content, "func Pretty(") {
				log.Printf("%s has Pretty statement outside of a function", path)
				exitCode = 16
			}
		}
		return nil
	})

	if err != nil {
		log.Printf("Error walking the path: %v", err)
		exitCode = 1
	}

	return exitCode
}

func runGoFmt() error {
	cmd := exec.Command("gofmt", "-l", "-w", ".")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("gofmt failed: %w", err)
	}

	return nil
}

func lint() error {
	if err := runGoFmt(); err != nil {
		return fmt.Errorf("gofmt checks failed: %w", err)
	}
	exitCode := checkGoFormat()
	if exitCode != 0 {
		return fmt.Errorf("custom Go format checks failed with exit code: %d", exitCode)
	}
	cmd := exec.Command("golangci-lint", "run", "--config", lintConfig)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("golangci-lint failed: %w", err)
	}

	return nil
}
