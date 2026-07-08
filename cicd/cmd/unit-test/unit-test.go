package unitTest

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

var UnitTestCmd = &cobra.Command{
	Use:   "unit-test",
	Short: "A cli used to control unit-test functionalities",
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true
		if err := runUnitTests(filtered, noCover, moduleFilter, shardFilter); err != nil {
			return err
		}
		return nil
	},
}

// fallbackWorkspaceModules is used only when go.work cannot be read/parsed. The
// authoritative module list is derived from go.work at runtime (see
// workspaceModuleList) so the tool, the CI matrix, and go.work cannot drift.
var fallbackWorkspaceModules = []string{".", "cicd", "core", "database", "hyperscaler", "lib", "vcm-proxy", "vcp-core"}

// moduleShardCounts sets how many parallel shards a module's packages are split
// into for the CI matrix. Large/slow modules are split; anything not listed runs
// as a single shard. Keyed by the normalized module path (".", "core", ...).
var moduleShardCounts = map[string]int{
	".":    3,
	"core": 3,
}

const coverageFile = "coverage.out"
const excludeFile = "./cicd/cmd/unit-test/exclude-from-code-coverage"
const goWorkFile = "go.work"

var (
	noCover          bool
	moduleFilter     string
	shardFilter      string
	mergeInputs      string
	mergeFiltered    bool
	mergeExpected    int
	listModulesCount bool
)

// MergeCmd merges per-module/sharded coverage profiles (each a `coverage.out`
// produced by an individual matrix job) into a single coverage.out. This lets CI
// run coverage in parallel across modules and stitch the results at the end.
var MergeCmd = &cobra.Command{
	Use:   "merge",
	Short: "Merge sharded coverage profiles into a single coverage.out",
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true
		return mergeCoverage(mergeInputs, mergeFiltered, mergeExpected)
	},
}

// mergeCoverage scans inputDir recursively for *.out coverage fragments,
// concatenates their entries under a single "mode: set" header, writes
// coverage.out, and optionally applies the exclude filter.
//
// expected, when > 0, is the number of coverage fragments that must be present
// (i.e. the CI matrix size). If fewer are found the merge fails instead of
// silently producing a coverage report from incomplete data (e.g. when a shard
// failed to upload its fragment).
func mergeCoverage(inputDir string, filter bool, expected int) error {
	if inputDir == "" {
		return fmt.Errorf("--inputs directory is required")
	}

	var files []string
	walkErr := filepath.WalkDir(inputDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".out") {
			files = append(files, path)
		}
		return nil
	})
	if walkErr != nil {
		return fmt.Errorf("failed to scan %s: %w", inputDir, walkErr)
	}
	if len(files) == 0 {
		return fmt.Errorf("no coverage fragments (*.out) found under %s", inputDir)
	}
	if expected > 0 && len(files) != expected {
		return fmt.Errorf("incomplete coverage data: found %d coverage fragment(s) under %s but expected %d (matrix shard count); refusing to merge partial coverage", len(files), inputDir, expected)
	}
	sort.Strings(files)

	var combined bytes.Buffer
	combined.WriteString("mode: set\n")
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return fmt.Errorf("failed to read %s: %w", f, err)
		}
		// Drop the per-fragment "mode:" header; keep only entries.
		for i, line := range strings.SplitAfter(string(data), "\n") {
			if i == 0 && strings.HasPrefix(line, "mode:") {
				continue
			}
			combined.WriteString(line)
		}
	}

	if err := os.WriteFile(coverageFile, combined.Bytes(), 0o644); err != nil {
		return fmt.Errorf("failed to write merged %s: %w", coverageFile, err)
	}
	log.Printf("Merged %d coverage fragment(s) into %s", len(files), coverageFile)

	if filter {
		if err := filterUnitTestFile(); err != nil {
			return fmt.Errorf("failed to filter merged coverage: %w", err)
		}
	}
	return nil
}

// runUnitTests runs the workspace unit tests.
//
//   - When noCover is true, tests run in "fast gate" mode: no coverage profile is
//     produced, which keeps results in Go's test cache so unchanged packages are
//     not re-run. This is the blocking PR signal and is meant to be fast.
//   - When noCover is false, a merged coverage.out profile is produced (used by the
//     coverage / pr-coverage jobs).
//
// module, when non-empty, restricts execution to a single workspace module. This
// lets CI shard the gate across one job per module (a matrix) for parallelism.
//
// shard, when non-empty (format "i/N"), runs only the i-th of N package shards of
// the selected module(s). This lets CI split a large module (e.g. core, root)
// across several parallel jobs to cut wall-clock time.
func runUnitTests(filtered, noCover bool, module, shard string) error {
	mods, err := selectModules(module)
	if err != nil {
		return err
	}

	shardIndex, shardTotal, err := parseShard(shard)
	if err != nil {
		return err
	}

	if noCover {
		if err := runGoTestsNoCover(mods, shardIndex, shardTotal); err != nil {
			log.Println("Error running Go tests:", err)
			return err
		}
		log.Println("Go unit tests completed successfully.")
		return nil
	}

	if err := runGoTests(mods, shardIndex, shardTotal); err != nil {
		log.Println("Error running Go tests:", err)
		return err
	}
	log.Println("Go unit tests completed successfully.")

	if filtered {
		if err := filterUnitTestFile(); err != nil {
			log.Println("Error filtering unit tests:", err)
			os.Exit(1)
		}
	}

	return nil
}

// selectModules returns the modules to test. An empty module selects all
// workspace modules; otherwise it validates and returns just that one.
func selectModules(module string) ([]string, error) {
	mods := workspaceModuleList()
	if module == "" {
		return mods, nil
	}
	for _, m := range mods {
		if m == module {
			return []string{m}, nil
		}
	}
	return nil, fmt.Errorf("unknown module %q; valid modules: %s", module, strings.Join(mods, ", "))
}

// workspaceModuleList returns the modules participating in the go workspace,
// parsed from go.work so the tool, CI matrix, Makefile, and go.work share a
// single source of truth. It falls back to a hard-coded list if go.work is
// unreadable.
func workspaceModuleList() []string {
	mods, err := parseGoWorkModules(goWorkFile)
	if err != nil || len(mods) == 0 {
		if err != nil {
			log.Printf("warning: falling back to built-in module list (could not read %s: %v)", goWorkFile, err)
		}
		return fallbackWorkspaceModules
	}
	return mods
}

// parseGoWorkModules extracts the module directories from a go.work file. It
// understands both the block form (`use ( ./a ./b )`) and the single-line form
// (`use ./a`). Paths are normalized: a leading "./" is stripped and "." is kept.
func parseGoWorkModules(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var mods []string
	inBlock := false
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if i := strings.Index(line, "//"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		if line == "" {
			continue
		}
		switch {
		case inBlock:
			if line == ")" {
				inBlock = false
				continue
			}
			mods = append(mods, normalizeModulePath(line))
		case strings.HasPrefix(line, "use ("):
			inBlock = true
		case strings.HasPrefix(line, "use "):
			mods = append(mods, normalizeModulePath(strings.TrimSpace(strings.TrimPrefix(line, "use"))))
		}
	}
	return mods, nil
}

// normalizeModulePath converts a go.work entry to the relative module dir used
// elsewhere in this tool: "." stays ".", and a leading "./" is stripped.
func normalizeModulePath(p string) string {
	p = strings.TrimSpace(p)
	if p == "." || p == "./" {
		return "."
	}
	return strings.TrimPrefix(p, "./")
}

// matrixTarget is one CI unit-test shard: a friendly name, the module it tests,
// and its shard spec ("i/N"). The CI matrix is built from a JSON list of these.
type matrixTarget struct {
	Name   string `json:"name"`
	Module string `json:"module"`
	Shard  string `json:"shard"`
}

// buildMatrixTargets expands the go.work module list into the CI matrix, applying
// moduleShardCounts so large modules become several shards. The root module (".")
// is named "root"; modules split into N>1 shards are named "<base>-<i>".
func buildMatrixTargets() []matrixTarget {
	var targets []matrixTarget
	for _, mod := range workspaceModuleList() {
		base := mod
		if mod == "." {
			base = "root"
		}
		n := moduleShardCounts[mod]
		if n < 1 {
			n = 1
		}
		if n == 1 {
			targets = append(targets, matrixTarget{Name: base, Module: mod, Shard: "1/1"})
			continue
		}
		for i := 1; i <= n; i++ {
			targets = append(targets, matrixTarget{
				Name:   fmt.Sprintf("%s-%d", base, i),
				Module: mod,
				Shard:  fmt.Sprintf("%d/%d", i, n),
			})
		}
	}
	return targets
}

// ListModulesCmd prints the CI unit-test matrix (derived from go.work and the
// shard policy) as a compact JSON array on stdout. CI consumes this to build the
// test matrix, so the matrix can never drift from go.work.
var ListModulesCmd = &cobra.Command{
	Use:   "list-modules",
	Short: "Print the unit-test CI matrix (from go.work) as a JSON array",
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true
		targets := buildMatrixTargets()
		if listModulesCount {
			fmt.Fprintln(os.Stdout, len(targets))
			return nil
		}
		out, err := json.Marshal(targets)
		if err != nil {
			return fmt.Errorf("failed to marshal matrix targets: %w", err)
		}
		fmt.Fprintln(os.Stdout, string(out))
		return nil
	},
}

// parseShard parses a shard spec of the form "i/N" (1-based). An empty spec
// means a single shard (1/1) covering all packages.
func parseShard(spec string) (int, int, error) {
	if strings.TrimSpace(spec) == "" {
		return 1, 1, nil
	}
	parts := strings.SplitN(spec, "/", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid --shard %q, expected format i/N", spec)
	}
	idx, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
	total, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err1 != nil || err2 != nil || total < 1 || idx < 1 || idx > total {
		return 0, 0, fmt.Errorf("invalid --shard %q, expected format i/N with 1<=i<=N", spec)
	}
	return idx, total, nil
}

// pkgWeight pairs a package import path with an estimated cost used to balance
// shards. The weight is the total size of the package's test files (a cheap,
// deterministic proxy for test runtime), plus a small base so test-less packages
// still get distributed.
type pkgWeight struct {
	importPath string
	weight     int64
}

// moduleTestTargets returns the `go test` package arguments for a module's shard
// and whether there is anything to run. For a single shard it returns ["./..."]
// (preserving the unsharded behavior). For N>1 it weighs each package by its
// test-file size and greedily bin-packs packages (heaviest first into the
// lightest shard — the LPT algorithm) so total weight (≈ runtime) is balanced
// across shards rather than just package count. The split is deterministic, so
// every shard job computes the same partition and runs only its bucket.
func moduleTestTargets(mod string, shardIndex, shardTotal int) ([]string, bool, error) {
	pkgs, err := listPackagesWithWeight(mod)
	if err != nil {
		return nil, false, err
	}
	if len(pkgs) == 0 {
		return nil, false, nil
	}
	if shardTotal <= 1 {
		return []string{"./..."}, true, nil
	}

	// Heaviest first; tie-break on import path for a deterministic order.
	sort.Slice(pkgs, func(i, j int) bool {
		if pkgs[i].weight != pkgs[j].weight {
			return pkgs[i].weight > pkgs[j].weight
		}
		return pkgs[i].importPath < pkgs[j].importPath
	})

	loads := make([]int64, shardTotal)
	buckets := make([][]string, shardTotal)
	for _, p := range pkgs {
		lightest := 0
		for b := 1; b < shardTotal; b++ {
			if loads[b] < loads[lightest] {
				lightest = b
			}
		}
		buckets[lightest] = append(buckets[lightest], p.importPath)
		loads[lightest] += p.weight
	}

	selected := buckets[shardIndex-1]
	if len(selected) == 0 {
		return nil, false, nil
	}
	return selected, true, nil
}

// listPackagesWithWeight lists a module's packages (with the same `test_exclude`
// build tag as the test run, so tag-excluded packages like scripts/sanity are
// omitted — naming them explicitly to `go test` would fail with "build
// constraints exclude all Go files", whereas `./...` silently skips them) and
// estimates each package's cost from the size of its test files. `go list ./...`
// exits non-zero with "matched no packages" for a module with no Go packages;
// that is treated as an empty (skippable) module rather than a hard failure.
func listPackagesWithWeight(mod string) ([]pkgWeight, error) {
	const sep = "\x1f"
	format := "{{.ImportPath}}" + sep + "{{.Dir}}" + sep + "{{join .TestGoFiles \",\"}}" + sep + "{{join .XTestGoFiles \",\"}}"
	listCmd := exec.Command("go", "list", "-tags=test_exclude", "-f", format, "./...")
	listCmd.Dir = mod
	var listOut, listErr bytes.Buffer
	listCmd.Stdout = &listOut
	listCmd.Stderr = &listErr
	if err := listCmd.Run(); err != nil {
		if strings.Contains(listErr.String(), "matched no packages") {
			return nil, nil
		}
		return nil, fmt.Errorf("go list failed in %s: %w; stderr: %s", mod, err, listErr.String())
	}

	var pkgs []pkgWeight
	for _, line := range strings.Split(strings.TrimSpace(listOut.String()), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Split(line, sep)
		if len(fields) < 4 {
			continue
		}
		importPath, dir := fields[0], fields[1]
		var weight int64 = 1 // base weight so test-less packages still spread
		for _, group := range fields[2:4] {
			if group == "" {
				continue
			}
			for _, name := range strings.Split(group, ",") {
				if fi, statErr := os.Stat(filepath.Join(dir, name)); statErr == nil {
					weight += fi.Size()
				}
			}
		}
		pkgs = append(pkgs, pkgWeight{importPath: importPath, weight: weight})
	}
	return pkgs, nil
}

// runGoTestsNoCover runs tests without a coverage profile so Go's test cache can
// short-circuit unchanged packages. It is the fast, cacheable gate path.
func runGoTestsNoCover(mods []string, shardIndex, shardTotal int) error {
	log.Println("Running Go unit tests (fast gate, cache-enabled) across workspace modules...")

	for _, mod := range mods {
		log.Printf("==> testing module %q (shard %d/%d)", mod, shardIndex, shardTotal)
		targets, run, err := moduleTestTargets(mod, shardIndex, shardTotal)
		if err != nil {
			return err
		}
		if !run {
			log.Printf("    (no packages for %q shard %d/%d, skipping)", mod, shardIndex, shardTotal)
			continue
		}

		args := append([]string{"test", "-tags=test_exclude"}, targets...)
		cmd := exec.Command("go", args...)
		cmd.Dir = mod
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		// Run against the FIPS 140-2 validated BoringSSL backend.
		cmd.Env = append(os.Environ(), "GOEXPERIMENT=boringcrypto")
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("go test failed in %s: %w", mod, err)
		}
	}

	log.Println("Go tests completed successfully.")
	return nil
}

func runGoTests(mods []string, shardIndex, shardTotal int) error {
	log.Println("Running Go unit tests with coverage across workspace modules...")

	if err := os.Remove(coverageFile); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to clear %s: %w", coverageFile, err)
	}

	var combined bytes.Buffer
	combined.WriteString("mode: set\n")

	for _, mod := range mods {
		log.Printf("==> testing module %q (shard %d/%d)", mod, shardIndex, shardTotal)
		targets, run, err := moduleTestTargets(mod, shardIndex, shardTotal)
		if err != nil {
			return err
		}
		if !run {
			log.Printf("    (no packages for %q shard %d/%d, skipping)", mod, shardIndex, shardTotal)
			continue
		}
		modCoverage := "coverage.tmp.out"
		var stdout, stderr bytes.Buffer
		args := append([]string{"test", "-tags=test_exclude"}, targets...)
		args = append(args, "-cover", "-coverprofile="+modCoverage)
		cmd := exec.Command("go", args...)
		cmd.Dir = mod
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		// Run against the FIPS 140-2 validated BoringSSL backend.
		cmd.Env = append(os.Environ(), "GOEXPERIMENT=boringcrypto")

		if err := cmd.Run(); err != nil {
			log.Printf("Error running Go tests in %q: %v", mod, err)
			log.Println("Stdout output:", stdout.String())
			log.Println("Stderr output:", stderr.String())
			return fmt.Errorf("go test failed in %s: %w", mod, err)
		}

		modPath := mod + string(os.PathSeparator) + modCoverage
		data, err := os.ReadFile(modPath)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("failed to read %s: %w", modPath, err)
		}
		// Drop the per-module "mode:" header; keep only entries.
		for i, line := range strings.SplitAfter(string(data), "\n") {
			if i == 0 && strings.HasPrefix(line, "mode:") {
				continue
			}
			combined.WriteString(line)
		}
		_ = os.Remove(modPath)
	}

	if err := os.WriteFile(coverageFile, combined.Bytes(), 0o644); err != nil {
		return fmt.Errorf("failed to write merged %s: %w", coverageFile, err)
	}

	log.Println("Go tests completed successfully.")
	return nil
}

func filterUnitTestFile() error {
	log.Println("Filtering files from coverage report...")

	// Open the exclude patterns file
	excludeFile, err := os.Open(excludeFile)
	if err != nil {
		return fmt.Errorf("failed to open exclude patterns file: %w", err)
	}
	defer func() {
		if err := excludeFile.Close(); err != nil {
			log.Printf("Error closing exclude file: %v", err)
		}
	}()

	// Read exclude patterns into a slice
	var excludePatterns []string
	scanner := bufio.NewScanner(excludeFile)
	for scanner.Scan() {
		excludePatterns = append(excludePatterns, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("error reading exclude patterns file: %w", err)
	}

	// Open the coverage file
	inputFile, err := os.Open(coverageFile)
	if err != nil {
		return fmt.Errorf("failed to open coverage file: %w", err)
	}
	defer func() {
		if err := inputFile.Close(); err != nil {
			log.Printf("Error closing input file: %v", err)
		}
	}()

	// Filter lines based on exclude patterns
	var buffer bytes.Buffer
	scanner = bufio.NewScanner(inputFile)
	for scanner.Scan() {
		line := scanner.Text()
		exclude := false
		for _, pattern := range excludePatterns {
			if strings.Contains(line, pattern) {
				exclude = true
				break
			}
		}
		if !exclude {
			buffer.WriteString(line + "\n")
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("error reading coverage file: %w", err)
	}

	// Write the filtered content back to the coverage file
	return os.WriteFile(coverageFile, buffer.Bytes(), 0644)
}

func init() {
	UnitTestCmd.Flags().BoolVarP(&filtered, "filtered", "f", false, "Filter the coverage report")
	UnitTestCmd.Flags().BoolVar(&noCover, "no-cover", false, "Fast gate mode: run tests without a coverage profile so Go's test cache is used")
	UnitTestCmd.Flags().StringVar(&moduleFilter, "module", "", "Restrict execution to a single workspace module (used by the CI matrix)")
	UnitTestCmd.Flags().StringVar(&shardFilter, "shard", "", "Run only the i-th of N package shards of the module, format i/N (used by the CI matrix to split large modules)")

	MergeCmd.Flags().StringVar(&mergeInputs, "inputs", "", "Directory containing per-module coverage fragments (*.out) to merge")
	MergeCmd.Flags().BoolVar(&mergeFiltered, "filtered", false, "Apply the exclude filter to the merged coverage report")
	MergeCmd.Flags().IntVar(&mergeExpected, "expected", 0, "Expected number of coverage fragments (CI matrix size); merge fails if fewer are found")

	ListModulesCmd.Flags().BoolVar(&listModulesCount, "count", false, "Print the number of matrix shards instead of the JSON matrix")

	UnitTestCmd.AddCommand(CoverageCmd)
	UnitTestCmd.AddCommand(MergeCmd)
	UnitTestCmd.AddCommand(ListModulesCmd)
}
