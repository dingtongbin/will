package main

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// metricValue is one measured value together with its unit.
type metricValue struct {
	value uint64
	unit  string
}

// runResult is what one program run reported.
type runResult struct {
	metrics      map[string]metricValue
	checks       map[string]uint64
	checksum     uint64
	hasChecksum  bool
	wall         time.Duration
	peakRSSBytes uint64
}

// series holds the values one metric took over the runs of a program.
type series struct {
	unit   string
	values []uint64
}

// runProgram runs one program once and parses the metric lines it printed.
// The program reports its own measurements, so the wall time of the process
// is only used by the scenarios that measure the process itself.
func runProgram(exe, dir string) (runResult, error) {
	result := runResult{metrics: map[string]metricValue{}, checks: map[string]uint64{}}
	cmd := exec.Command(exe)
	cmd.Dir = dir
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	start := time.Now()
	if err := cmd.Start(); err != nil {
		return result, fmt.Errorf("%s: the program failed: %v", exe, err)
	}
	tracker := newMemoryTracker()
	tracker.attach(cmd)
	err := cmd.Wait()
	result.wall = time.Since(start)
	result.peakRSSBytes = tracker.peakBytes()
	if err != nil {
		return result, fmt.Errorf("%s: the program failed: %v\n%s", exe, err, output.String())
	}
	scanner := bufio.NewScanner(strings.NewReader(output.String()))
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		switch {
		case strings.HasPrefix(line, "bench metric "):
			fields := strings.Fields(line)
			if len(fields) < 4 {
				return result, fmt.Errorf("%s: the metric line is malformed: %s", exe, line)
			}
			value, err := strconv.ParseUint(fields[3], 10, 64)
			if err != nil {
				return result, fmt.Errorf("%s: the metric value is not a number: %s", exe, line)
			}
			unit := ""
			if len(fields) > 4 {
				unit = fields[4]
			}
			result.metrics[fields[2]] = metricValue{value: value, unit: unit}
		case strings.HasPrefix(line, "bench checksum "):
			fields := strings.Fields(line)
			if len(fields) < 3 {
				return result, fmt.Errorf("%s: the checksum line is malformed: %s", exe, line)
			}
			value, err := strconv.ParseUint(fields[2], 10, 64)
			if err != nil {
				return result, fmt.Errorf("%s: the checksum is not a number: %s", exe, line)
			}
			result.checksum = value
			result.hasChecksum = true
			result.checks["checksum"] = value
		case strings.HasPrefix(line, "bench check "):
			fields := strings.Fields(line)
			if len(fields) < 4 {
				return result, fmt.Errorf("%s: the check line is malformed: %s", exe, line)
			}
			value, err := strconv.ParseUint(fields[3], 10, 64)
			if err != nil {
				return result, fmt.Errorf("%s: the check is not a number: %s", exe, line)
			}
			result.checks[fields[2]] = value
		}
	}
	if err := scanner.Err(); err != nil {
		return result, err
	}
	return result, nil
}

// checkValue is one check of a program: the value and whether every run
// reported the same one.
type checkValue struct {
	value uint64
	same  bool
}

// aggregate turns the runs of one program into per metric series. The
// checks of the runs must agree, because a program that computes different
// results on different runs is not measuring anything.
func aggregate(results []runResult) (map[string]series, map[string]checkValue) {
	out := map[string]series{}
	checks := map[string]checkValue{}
	for _, result := range results {
		for name, value := range result.metrics {
			s := out[name]
			s.unit = value.unit
			s.values = append(s.values, value.value)
			out[name] = s
		}
		for name, value := range result.checks {
			first, seen := checks[name]
			if !seen {
				checks[name] = checkValue{value: value, same: true}
				continue
			}
			if first.value != value {
				checks[name] = checkValue{value: first.value, same: false}
			}
		}
	}
	return out, checks
}

// percentile returns the nearest rank percentile of an ascending series. The
// rule is the one the shared support uses: the sample at ceil(count * num /
// den), counted from one.
func percentile(values []uint64, num, den uint64) uint64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]uint64(nil), values...)
	for i := 1; i < len(sorted); i++ {
		value := sorted[i]
		j := i - 1
		for j >= 0 && sorted[j] > value {
			sorted[j+1] = sorted[j]
			j--
		}
		sorted[j+1] = value
	}
	rank := uint64(len(sorted)) * num
	rank = (rank + den - 1) / den
	if rank < 1 {
		rank = 1
	}
	if rank > uint64(len(sorted)) {
		rank = uint64(len(sorted))
	}
	return sorted[rank-1]
}

// maxOf returns the largest value of a series.
func maxOf(values []uint64) uint64 {
	var out uint64
	for _, value := range values {
		if value > out {
			out = value
		}
	}
	return out
}

// lineCount counts the source lines of a file, which the compile scenarios
// use to report the compile throughput.
func lineCount(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	count := 0
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}
	return count
}
