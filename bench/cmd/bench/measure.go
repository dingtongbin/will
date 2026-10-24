package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// measure runs the selected scenarios and writes their results.
func measure(root string, opts options, stdout, stderr *os.File) int {
	cc, err := findCompiler(opts)
	if err != nil {
		fmt.Fprintf(stderr, "bench: %v\n", err)
		return 1
	}
	will, willErr := findWill(opts)
	if willErr != nil {
		fmt.Fprintf(stderr, "bench: %v; the will side of every scenario is skipped\n", willErr)
	}
	// The will compiler drives clang itself, so the tool tells it which
	// clang to use when the environment does not say.
	if os.Getenv("WILL_CLANG") == "" && strings.Contains(filepath.Base(cc), "clang") {
		os.Setenv("WILL_CLANG", cc)
	}
	repo, repoErr := findRepoRoot(root)
	if repoErr != nil && willErr == nil {
		fmt.Fprintf(stderr, "bench: %v; the will side of every scenario is skipped\n", repoErr)
		will = ""
	}
	dir := opts.out
	if dir == "" {
		dir = filepath.Join(root, "results", targetTriple())
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintf(stderr, "bench: %v\n", err)
		return 1
	}
	buildDir, err := os.MkdirTemp("", "will-bench")
	if err != nil {
		fmt.Fprintf(stderr, "bench: %v\n", err)
		return 1
	}
	if !opts.keep {
		defer os.RemoveAll(buildDir)
	} else {
		fmt.Fprintf(stdout, "bench: the build directory is %s\n", buildDir)
	}
	ccVersion := compilerVersion(cc)
	willIdentity := ""
	if will != "" {
		willIdentity = willCompilerIdentity(will)
	}
	machine := machineID()
	failed := 0
	for _, sc := range selectScenarios(opts.scopes) {
		rows, err := measureScenario(root, repo, sc, opts, cc, ccVersion, will, willIdentity, machine, buildDir, stderr)
		if err != nil {
			fmt.Fprintf(stderr, "bench: %s: %v\n", sc.name, err)
			failed++
			continue
		}
		path := resultPath(dir, sc)
		if err := writeResults(path, rows); err != nil {
			fmt.Fprintf(stderr, "bench: %s: %v\n", sc.name, err)
			failed++
			continue
		}
		fmt.Fprintf(stdout, "bench: %s: %d rows -> %s\n", sc.name, len(rows), path)
	}
	info := describeMachine(cc, will)
	if err := writeMachine(filepath.Join(dir, "machine.txt"), info); err != nil {
		fmt.Fprintf(stderr, "bench: %v\n", err)
		failed++
	}
	if failed > 0 {
		return 1
	}
	return 0
}

// measureScenario builds and runs one scenario and returns its result rows.
func measureScenario(root, repo string, sc scenario, opts options, cc, ccVersion, will, willIdentity, machine, buildDir string, stderr *os.File) ([]resultRow, error) {
	switch sc.kind {
	case kindProgram:
		return measureProgram(root, repo, sc, opts, cc, ccVersion, will, willIdentity, machine, buildDir, stderr)
	case kindProcess:
		return measureProcess(root, repo, sc, opts, cc, ccVersion, will, willIdentity, machine, buildDir, stderr)
	case kindSize:
		return measureSize(root, repo, sc, opts, cc, ccVersion, will, willIdentity, machine, buildDir, stderr)
	case kindCompile:
		return measureCompile(root, repo, sc, opts, cc, ccVersion, will, willIdentity, machine, buildDir, stderr)
	}
	return nil, fmt.Errorf("the kind %q is not known", sc.kind)
}

// measureProgram runs both sides of a scenario and collects their metrics.
func measureProgram(root, repo string, sc scenario, opts options, cc, ccVersion, will, willIdentity, machine, buildDir string, stderr *os.File) ([]resultRow, error) {
	cExe := filepath.Join(buildDir, "c_"+strings.ReplaceAll(sc.name, "/", "_"))
	if _, err := buildC(root, sc, cc, cExe); err != nil {
		return nil, err
	}
	cResults, err := runMany(cExe, filepath.Dir(filepath.Join(root, filepath.FromSlash(sc.cFile))), opts.runs)
	if err != nil {
		return nil, err
	}
	cMetrics, cChecks := aggregate(cResults)
	var rows []resultRow
	for name, s := range cMetrics {
		rows = append(rows, metricRow(machine, ccVersion, sc, name, s, opts.runs, sc.note))
	}
	if sc.willDir == "" || will == "" {
		return rows, nil
	}
	willExe := filepath.Join(buildDir, "will_"+strings.ReplaceAll(sc.name, "/", "_"))
	if _, err := buildWill(root, repo, will, sc, willExe); err != nil {
		return nil, err
	}
	willResults, err := runMany(willExe, filepath.Join(root, filepath.FromSlash(sc.willDir)), opts.runs)
	if err != nil {
		return nil, err
	}
	willMetrics, willChecks := aggregate(willResults)
	if err := compareChecks(sc.name, cChecks, willChecks); err != nil {
		return nil, err
	}
	for name, s := range willMetrics {
		rows = append(rows, metricRow(machine, willIdentity, sc, name, s, opts.runs, sc.note))
	}
	return rows, nil
}

// compareChecks reports the checks the two sides of a scenario do not share.
// The checks prove that both sides did the same work. A check that only one
// side reports is not compared, because a scenario may measure more than the
// language side can measure.
func compareChecks(name string, cChecks, willChecks map[string]checkValue) error {
	for check, value := range cChecks {
		other, ok := willChecks[check]
		if !ok {
			return fmt.Errorf("%s: the will side does not report the check %q", name, check)
		}
		if !value.same || !other.same {
			return fmt.Errorf("%s: the check %q is not the same in every run", name, check)
		}
		if value.value != other.value {
			return fmt.Errorf("%s: the check %q differs: the C side has %d, the will side has %d",
				name, check, value.value, other.value)
		}
	}
	for check := range willChecks {
		if _, ok := cChecks[check]; !ok {
			return fmt.Errorf("%s: the C side does not report the check %q", name, check)
		}
	}
	return nil
}

// measureProcess measures the time the process of a program takes.
func measureProcess(root, repo string, sc scenario, opts options, cc, ccVersion, will, willIdentity, machine, buildDir string, stderr *os.File) ([]resultRow, error) {
	cExe := filepath.Join(buildDir, "c_"+strings.ReplaceAll(sc.name, "/", "_"))
	if _, err := buildC(root, sc, cc, cExe); err != nil {
		return nil, err
	}
	cResults, err := runMany(cExe, filepath.Dir(filepath.Join(root, filepath.FromSlash(sc.cFile))), opts.runs)
	if err != nil {
		return nil, err
	}
	var rows []resultRow
	rows = append(rows, processRow(machine, ccVersion, sc, cResults, opts.runs))
	if sc.willDir == "" || will == "" {
		return rows, nil
	}
	willExe := filepath.Join(buildDir, "will_"+strings.ReplaceAll(sc.name, "/", "_"))
	if _, err := buildWill(root, repo, will, sc, willExe); err != nil {
		return nil, err
	}
	willResults, err := runMany(willExe, filepath.Join(root, filepath.FromSlash(sc.willDir)), opts.runs)
	if err != nil {
		return nil, err
	}
	rows = append(rows, processRow(machine, willIdentity, sc, willResults, opts.runs))
	return rows, nil
}

// measureSize measures the size of the executables of a scenario.
func measureSize(root, repo string, sc scenario, opts options, cc, ccVersion, will, willIdentity, machine, buildDir string, stderr *os.File) ([]resultRow, error) {
	cExe := filepath.Join(buildDir, "c_"+strings.ReplaceAll(sc.name, "/", "_"))
	if _, err := buildC(root, sc, cc, cExe); err != nil {
		return nil, err
	}
	var rows []resultRow
	rows = append(rows, sizeRow(machine, ccVersion, sc, cExe, 1))
	if sc.willDir == "" || will == "" {
		return rows, nil
	}
	willExe := filepath.Join(buildDir, "will_"+strings.ReplaceAll(sc.name, "/", "_"))
	if _, err := buildWill(root, repo, will, sc, willExe); err != nil {
		return nil, err
	}
	rows = append(rows, sizeRow(machine, willIdentity, sc, willExe, 1))
	return rows, nil
}

// measureCompile measures the build of both sides of a scenario.
func measureCompile(root, repo string, sc scenario, opts options, cc, ccVersion, will, willIdentity, machine, buildDir string, stderr *os.File) ([]resultRow, error) {
	var rows []resultRow
	cLines := cSourceLines(root, sc)
	cBuild := filepath.Join(buildDir, "c_"+strings.ReplaceAll(sc.name, "/", "_"))
	var cBuildMs, cLineRates, cRSS, cIncremental []uint64
	for i := 0; i < opts.runs; i++ {
		outcome := buildCProgram(root, sc, cc, cBuild, buildDir, false)
		if outcome.err != nil {
			return nil, outcome.err
		}
		cBuildMs = append(cBuildMs, uint64(outcome.duration.Microseconds())/1000)
		cLineRates = append(cLineRates, linesPerSecond(cLines, outcome.duration))
		cRSS = append(cRSS, outcome.peakRSS)
		if sc.touchC != "" {
			inc := buildCProgram(root, sc, cc, cBuild, buildDir, true)
			if inc.err != nil {
				return nil, inc.err
			}
			cIncremental = append(cIncremental, uint64(inc.duration.Microseconds())/1000)
		}
	}
	rows = append(rows, compileSeriesRow(machine, ccVersion, sc, "build_ms", cBuildMs, opts.runs))
	rows = append(rows, compileSeriesRow(machine, ccVersion, sc, "lines_per_second", cLineRates, opts.runs))
	rows = append(rows, compileSeriesRow(machine, ccVersion, sc, "build_peak_rss", cRSS, opts.runs))
	if len(cIncremental) != 0 {
		rows = append(rows, compileSeriesRow(machine, ccVersion, sc, "incremental_ms", cIncremental, opts.runs))
	}
	if sc.willDir == "" || will == "" {
		return rows, nil
	}
	willLines := willSourceLines(root, sc)
	willBuild := filepath.Join(buildDir, "will_"+strings.ReplaceAll(sc.name, "/", "_"))
	var wBuildMs, wLineRates, wRSS, wIncremental []uint64
	for i := 0; i < opts.runs; i++ {
		outcome := buildWillProgram(root, repo, will, sc, willBuild, false)
		if outcome.err != nil {
			return nil, outcome.err
		}
		wBuildMs = append(wBuildMs, uint64(outcome.duration.Microseconds())/1000)
		wLineRates = append(wLineRates, linesPerSecond(willLines, outcome.duration))
		wRSS = append(wRSS, outcome.peakRSS)
		if sc.touchW != "" {
			inc := buildWillProgram(root, repo, will, sc, willBuild, true)
			if inc.err != nil {
				return nil, inc.err
			}
			wIncremental = append(wIncremental, uint64(inc.duration.Microseconds())/1000)
		}
	}
	rows = append(rows, compileSeriesRow(machine, willIdentity, sc, "build_ms", wBuildMs, opts.runs))
	rows = append(rows, compileSeriesRow(machine, willIdentity, sc, "lines_per_second", wLineRates, opts.runs))
	rows = append(rows, compileSeriesRow(machine, willIdentity, sc, "build_peak_rss", wRSS, opts.runs))
	if len(wIncremental) != 0 {
		rows = append(rows, compileSeriesRow(machine, willIdentity, sc, "incremental_ms", wIncremental, opts.runs))
	}
	return rows, nil
}

// buildOutcome is what one build cost.
type buildOutcome struct {
	duration time.Duration
	peakRSS  uint64
	err      error
}

// buildCProgram builds the C side of a compile scenario. With incremental
// set, only the touched file is compiled again and the objects are linked.
func buildCProgram(root string, sc scenario, cc string, out string, buildDir string, incremental bool) buildOutcome {
	objects := filepath.Join(buildDir, "obj_"+strings.ReplaceAll(sc.name, "/", "_"))
	if err := os.MkdirAll(objects, 0o755); err != nil {
		return buildOutcome{err: err}
	}
	sources := append([]string{sc.cFile}, sc.cExtra...)
	names := make([]string, 0, len(sources))
	for _, source := range sources {
		name := strings.TrimSuffix(filepath.Base(source), ".c") + ".o"
		names = append(names, filepath.Join(objects, name))
	}
	start := time.Now()
	var peak uint64
	for i, source := range sources {
		if incremental && sc.touchC != "" && source != sc.touchC {
			continue
		}
		cmd := exec.Command(cc, "-O2", "-I", filepath.Join(root, "csupport"), "-c",
			filepath.Join(root, filepath.FromSlash(source)), "-o", names[i])
		cmd.Dir = root
		tracker := newMemoryTracker()
		if err := cmd.Start(); err != nil {
			return buildOutcome{err: fmt.Errorf("the C build failed: %v", err)}
		}
		tracker.attach(cmd)
		if err := cmd.Wait(); err != nil {
			return buildOutcome{err: fmt.Errorf("the C build failed: %v", err)}
		}
		if bytes := tracker.peakBytes(); bytes > peak {
			peak = bytes
		}
	}
	link := exec.Command(cc, append(names, "-o", out)...)
	link.Dir = root
	if err := link.Run(); err != nil {
		return buildOutcome{err: fmt.Errorf("the C link failed: %v", err)}
	}
	return buildOutcome{duration: time.Since(start), peakRSS: peak}
}

// buildWillProgram builds the will side of a compile scenario. With
// incremental set, the touched file changes first, so the build has to
// rebuild that package and reuse the rest.
func buildWillProgram(root, repo string, will string, sc scenario, out string, incremental bool) buildOutcome {
	module := filepath.Join(root, filepath.FromSlash(sc.willDir))
	start := time.Now()
	if incremental && sc.touchW != "" {
		touch := filepath.Join(root, filepath.FromSlash(sc.touchW))
		if err := touchFile(touch); err != nil {
			return buildOutcome{err: err}
		}
	}
	cmd := exec.Command(will, "build", "-O2", module, "-o", out)
	cmd.Dir = repo
	cmd.Env = append(os.Environ(),
		"WILL_RUNTIME="+filepath.Join(repo, "runtime"))
	tracker := newMemoryTracker()
	if err := cmd.Start(); err != nil {
		return buildOutcome{err: fmt.Errorf("the will build failed: %v", err)}
	}
	tracker.attach(cmd)
	err := cmd.Wait()
	outcome := buildOutcome{duration: time.Since(start), peakRSS: tracker.peakBytes()}
	if err != nil {
		outcome.err = fmt.Errorf("the will build failed: %v", err)
	}
	return outcome
}

// touchFile changes the modification time of a file, so the build sees it as
// changed.
func touchFile(path string) error {
	if _, err := os.Stat(path); err != nil {
		return err
	}
	now := time.Now()
	return os.Chtimes(path, now, now)
}

// cSourceLines counts the source lines of the C side of a compile scenario.
func cSourceLines(root string, sc scenario) int {
	total := lineCount(filepath.Join(root, filepath.FromSlash(sc.cFile)))
	for _, extra := range sc.cExtra {
		total += lineCount(filepath.Join(root, filepath.FromSlash(extra)))
	}
	return total
}

// willSourceLines counts the source lines of the will side of a compile
// scenario.
func willSourceLines(root string, sc scenario) int {
	module := filepath.Join(root, filepath.FromSlash(sc.willDir))
	total := 0
	_ = filepath.WalkDir(module, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(entry.Name(), ".will") {
			return nil
		}
		total += lineCount(path)
		return nil
	})
	return total
}

// linesPerSecond returns the compile throughput of a build.
func linesPerSecond(lines int, duration time.Duration) uint64 {
	if duration <= 0 {
		return 0
	}
	return uint64(float64(lines) / duration.Seconds())
}

// runMany runs one program the given number of times.
func runMany(exe, dir string, runs int) ([]runResult, error) {
	var out []runResult
	for i := 0; i < runs; i++ {
		result, err := runProgram(exe, dir)
		if err != nil {
			return nil, err
		}
		out = append(out, result)
	}
	return out, nil
}

// metricRow builds one result row of a measured metric.
func metricRow(machine, compiler string, sc scenario, name string, s series, runs int, note string) resultRow {
	return resultRow{
		machine:  cleanField(machine),
		target:   targetTriple(),
		compiler: cleanField(compiler),
		opt:      "-O2",
		scenario: sc.name,
		metric:   name,
		unit:     s.unit,
		p50:      strconv.FormatUint(percentile(s.values, 50, 100), 10),
		p95:      strconv.FormatUint(percentile(s.values, 95, 100), 10),
		p99:      strconv.FormatUint(percentile(s.values, 99, 100), 10),
		max:      strconv.FormatUint(maxOf(s.values), 10),
		runs:     runs,
		date:     time.Now().Format("2006-01-02"),
		note:     cleanField(note),
	}
}

// processRow builds the row of a scenario that measures the process itself.
func processRow(machine, compiler string, sc scenario, results []runResult, runs int) resultRow {
	var values []uint64
	for _, result := range results {
		values = append(values, uint64(result.wall.Microseconds()))
	}
	s := series{unit: "us", values: values}
	return metricRow(machine, compiler, sc, "startup_us", s, runs, sc.note)
}

// sizeRow builds the row of a scenario that measures the executable size.
func sizeRow(machine, compiler string, sc scenario, exe string, runs int) resultRow {
	s := series{unit: "bytes", values: []uint64{fileSize(exe)}}
	return metricRow(machine, compiler, sc, "executable_bytes", s, runs, sc.note)
}

// fileSize returns the size of the file in bytes. The C compiler adds the
// suffix of the platform to the name it is given, so the name without the
// suffix is tried as well.
func fileSize(path string) uint64 {
	info, err := os.Stat(path)
	if err != nil {
		info, err = os.Stat(path + ".exe")
	}
	if err != nil {
		return 0
	}
	return uint64(info.Size())
}

// compileRow builds the row of a scenario that measures the build.
func compileSeriesRow(machine, compiler string, sc scenario, name string, values []uint64, runs int) resultRow {
	s := series{unit: compileUnit(name), values: values}
	return metricRow(machine, compiler, sc, name, s, runs, sc.note)
}

// compileUnit returns the unit of a build metric.
func compileUnit(name string) string {
	switch name {
	case "build_ms", "incremental_ms":
		return "ms"
	case "lines_per_second":
		return "lines/s"
	case "build_peak_rss":
		return "bytes"
	}
	return "count"
}

// willCompilerIdentity returns the identity of the will compiler: the
// version and the hash of the binary, so a rebuilt compiler is visible.
func willCompilerIdentity(will string) string {
	output, err := exec.Command(will, "version").Output()
	if err != nil {
		return "will"
	}
	version := ""
	hash := ""
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		switch fields[0] {
		case "will":
			version = fields[1]
		case "binary":
			hash = fields[1]
		}
	}
	if version == "" {
		return "will"
	}
	if len(hash) > 12 {
		hash = hash[:12]
	}
	if hash == "" {
		return "will " + version
	}
	return "will " + version + " " + hash
}

// machineID names the machine the results come from.
func machineID() string {
	return targetTriple() + " " + cpuModel()
}
