package main

import (
	"encoding/csv"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// resultRow is one row of a result csv. The columns are fixed by the method
// volume: machine, target, compiler, opt, scenario, metric, unit, p50, p95,
// p99, max, runs, date, note.
type resultRow struct {
	machine  string
	target   string
	compiler string
	opt      string
	scenario string
	metric   string
	unit     string
	p50      string
	p95      string
	p99      string
	max      string
	runs     int
	date     string
	note     string
}

// csvHeader is the fixed column order of a result csv.
var csvHeader = []string{"machine", "target", "compiler", "opt", "scenario", "metric", "unit",
	"p50", "p95", "p99", "max", "runs", "date", "note"}

// writeResults writes the rows of one scenario to its csv.
func writeResults(path string, rows []resultRow) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	writer := csv.NewWriter(file)
	if err := writer.Write(csvHeader); err != nil {
		return err
	}
	for _, row := range rows {
		record := []string{row.machine, row.target, row.compiler, row.opt, row.scenario, row.metric,
			row.unit, row.p50, row.p95, row.p99, row.max, strconv.Itoa(row.runs), row.date, row.note}
		if err := writer.Write(record); err != nil {
			return err
		}
	}
	writer.Flush()
	return writer.Error()
}

// readResults reads the rows of one csv.
func readResults(path string) ([]resultRow, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	reader := csv.NewReader(file)
	records, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, nil
	}
	var rows []resultRow
	for _, record := range records[1:] {
		if len(record) < len(csvHeader) {
			continue
		}
		runs, _ := strconv.Atoi(record[11])
		rows = append(rows, resultRow{
			machine:  record[0],
			target:   record[1],
			compiler: record[2],
			opt:      record[3],
			scenario: record[4],
			metric:   record[5],
			unit:     record[6],
			p50:      record[7],
			p95:      record[8],
			p99:      record[9],
			max:      record[10],
			runs:     runs,
			date:     record[12],
			note:     record[13],
		})
	}
	return rows, nil
}

// cleanField removes the characters that would break a csv line.
func cleanField(text string) string {
	text = strings.ReplaceAll(text, ",", " ")
	text = strings.ReplaceAll(text, "\n", " ")
	return strings.TrimSpace(text)
}

// resultFiles lists the csv files of a results directory. The scenario names
// hold a slash, so the files live in a directory per scenario.
func resultFiles(dir string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.HasSuffix(path, ".csv") {
			files = append(files, path)
		}
		return nil
	})
	return files, err
}

// summaryEntry is one line of the summary table.
type summaryEntry struct {
	scenario string
	metric   string
	unit     string
	willP50  string
	cP50     string
	ratio    string
}

// summarize prints the table of the recorded results. It reads the csv files
// and prints their numbers; it never changes them.
func summarize(root string, opts options, stdout, stderr *os.File) int {
	dir := opts.out
	if dir == "" {
		dir = filepath.Join(root, "results", targetTriple())
	}
	files, err := resultFiles(dir)
	if err != nil || len(files) == 0 {
		fmt.Fprintf(stderr, "bench: no results in %s\n", dir)
		return 1
	}
	sort.Strings(files)
	values := map[string]map[string]summaryEntry{}
	for _, file := range files {
		rows, err := readResults(file)
		if err != nil {
			fmt.Fprintf(stderr, "bench: %v\n", err)
			return 1
		}
		for _, row := range rows {
			key := row.scenario + "|" + row.metric
			if values[key] == nil {
				values[key] = map[string]summaryEntry{}
			}
			entry := values[key][row.compiler]
			entry.scenario = row.scenario
			entry.metric = row.metric
			entry.unit = row.unit
			if strings.HasPrefix(row.compiler, "will") {
				entry.willP50 = row.p50
			} else {
				entry.cP50 = row.p50
			}
			values[key][row.compiler] = entry
		}
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	fmt.Fprintf(stdout, "%-22s %-14s %-8s %14s %14s %8s\n", "scenario", "metric", "unit", "will", "c", "ratio")
	for _, key := range keys {
		group := values[key]
		will := ""
		c := ""
		unit := ""
		for _, entry := range group {
			if entry.willP50 != "" {
				will = entry.willP50
			}
			if entry.cP50 != "" {
				c = entry.cP50
			}
			if entry.unit != "" {
				unit = entry.unit
			}
		}
		ratio := "-"
		if ratioUnit(unit) {
			ratio = ratioOf(will, c)
		}
		first := group[firstCompiler(group)]
		fmt.Fprintf(stdout, "%-22s %-14s %-8s %14s %14s %8s\n", first.scenario, first.metric, unit, will, c, ratio)
	}
	return 0
}

// firstCompiler returns any compiler of the group, so the table can name the
// scenario and the metric.
func firstCompiler(group map[string]summaryEntry) string {
	names := make([]string, 0, len(group))
	for name := range group {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return ""
	}
	return names[0]
}

// ratioUnit reports whether a will/C ratio is meaningful for this unit. Counts
// and byte sizes are not speeds and must not be printed as if they were.
func ratioUnit(unit string) bool {
	switch unit {
	case "ns", "us", "ms", "s", "lines/s":
		return true
	}
	return false
}

// ratioOf returns the will to c ratio of two p50 values, or a dash when one
// side is missing.
func ratioOf(will, c string) string {
	if will == "" || c == "" {
		return "-"
	}
	willValue, err := strconv.ParseFloat(will, 64)
	if err != nil {
		return "-"
	}
	cValue, err := strconv.ParseFloat(c, 64)
	if err != nil {
		return "-"
	}
	if cValue == 0 {
		return "-"
	}
	return fmt.Sprintf("%.3f", willValue/cValue)
}
