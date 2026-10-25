//go:build ignore

// Command tzgen compiles the IANA time zone database into the C table that
// std/time links. It reads the zoneinfo.zip that ships with the Go toolchain,
// expands the POSIX rule that follows the explicit transitions, and writes
// std/time/tzdata.c. Run it from the repository root:
//
//	go run std/time/tzgen/main.go
//
// The generated table covers 1900 through the whole Instant range. Every
// transition is checked against Go's own zone lookup before the file is
// written, so a parser mistake fails here rather than in a time test.
package main

import (
	"archive/zip"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// tableStart is the first Instant the i64 nanosecond count can represent. The
// table therefore covers the whole Instant range, not just modern dates.
const tableStart = int64(-9223372036)

// tableEnd is past the last representable Instant (2262-04-11).
const tableEnd = int64(9466848000)

// zoneType is one local time type in a TZif file.
type zoneType struct {
	offset int32
	isdst  bool
}

// transition is one offset change, in Unix seconds.
type transition struct {
	at     int64
	offset int32
}

// zone is one named row of the generated table.
type zone struct {
	name    string
	initial int32
	first   int
	count   int
}

// ruleDate is one side of a POSIX TZ rule.
type ruleDate struct {
	kind    byte // 'J', 'n', or 'M'
	month   int
	week    int
	weekday int
	day     int
	second  int64
}

// posixRule is the parsed footer of a version 2 TZif file.
type posixRule struct {
	hasDST bool
	stdOff int32
	dstOff int32
	start  ruleDate
	end    ruleDate
}

func be32(b []byte) int32 { return int32(binary.BigEndian.Uint32(b)) }
func be64(b []byte) int64 { return int64(binary.BigEndian.Uint64(b)) }

// readCounts returns the six counts of a TZif header that starts at base.
func readCounts(data []byte, base int) (timecnt, typecnt, charcnt, leapcnt, ttisstd, ttisgmt int, ok bool) {
	if base+44 > len(data) {
		return 0, 0, 0, 0, 0, 0, false
	}
	ttisgmt = int(be32(data[base+20:]))
	ttisstd = int(be32(data[base+24:]))
	leapcnt = int(be32(data[base+28:]))
	timecnt = int(be32(data[base+32:]))
	typecnt = int(be32(data[base+36:]))
	charcnt = int(be32(data[base+40:]))
	return timecnt, typecnt, charcnt, leapcnt, ttisstd, ttisgmt, true
}

// parseTZif returns the transitions in the covered range, the offset in effect
// just before the first one, and the POSIX rule from the v2 footer.
func parseTZif(data []byte) ([]transition, int32, string, bool) {
	if len(data) < 44 || string(data[:4]) != "TZif" {
		return nil, 0, "", false
	}
	version := data[4]
	timecnt, typecnt, charcnt, leapcnt, ttisstd, ttisgmt, ok := readCounts(data, 0)
	if !ok || typecnt == 0 {
		return nil, 0, "", false
	}
	off := 44
	if version >= '2' {
		off = 44 + timecnt*4 + timecnt + typecnt*6 + charcnt + leapcnt*8 + ttisstd + ttisgmt
		timecnt, typecnt, charcnt, leapcnt, ttisstd, ttisgmt, ok = readCounts(data, off)
		if !ok || typecnt == 0 {
			return nil, 0, "", false
		}
		off += 44
	}
	timeSize := 4
	if version >= '2' {
		timeSize = 8
	}
	if off+timecnt*timeSize+timecnt+typecnt*6+charcnt > len(data) {
		return nil, 0, "", false
	}
	times := make([]int64, timecnt)
	for i := 0; i < timecnt; i++ {
		if timeSize == 8 {
			times[i] = be64(data[off+i*8:])
		} else {
			times[i] = int64(be32(data[off+i*4:]))
		}
	}
	off += timecnt * timeSize
	indices := data[off : off+timecnt]
	off += timecnt
	types := make([]zoneType, typecnt)
	for i := 0; i < typecnt; i++ {
		types[i].offset = be32(data[off+i*6:])
		types[i].isdst = data[off+i*6+4] != 0
	}
	off += typecnt * 6
	off += charcnt
	off += leapcnt * (timeSize + 4)
	off += ttisstd
	off += ttisgmt
	footer := ""
	if version >= '2' && off < len(data) && data[off] == '\n' {
		off++
		end := off
		for end < len(data) && data[end] != '\n' {
			end++
		}
		footer = string(data[off:end])
	}
	initial := 0
	for i := 0; i < typecnt; i++ {
		if !types[i].isdst {
			initial = i
			break
		}
	}
	var out []transition
	prev := types[initial].offset
	initialAtStart := prev
	for i := 0; i < timecnt; i++ {
		index := int(indices[i])
		if index < 0 || index >= typecnt {
			return nil, 0, "", false
		}
		if times[i] <= tableStart {
			prev = types[index].offset
			initialAtStart = prev
			continue
		}
		if times[i] >= tableEnd {
			break
		}
		out = append(out, transition{at: times[i], offset: types[index].offset})
		prev = types[index].offset
	}
	return out, initialAtStart, footer, true
}

// parseOffset reads a POSIX offset and returns seconds west of Greenwich.
func parseOffset(s string, pos int) (int32, int, bool) {
	sign := int32(1)
	if pos < len(s) && (s[pos] == '+' || s[pos] == '-') {
		if s[pos] == '-' {
			sign = -1
		}
		pos++
	}
	hours := int32(0)
	digits := 0
	for pos < len(s) && s[pos] >= '0' && s[pos] <= '9' {
		hours = hours*10 + int32(s[pos]-'0')
		pos++
		digits++
	}
	if digits == 0 {
		return 0, pos, false
	}
	minutes := int32(0)
	seconds := int32(0)
	if pos < len(s) && s[pos] == ':' {
		pos++
		digits = 0
		for pos < len(s) && s[pos] >= '0' && s[pos] <= '9' {
			minutes = minutes*10 + int32(s[pos]-'0')
			pos++
			digits++
		}
		if digits == 0 {
			return 0, pos, false
		}
		if pos < len(s) && s[pos] == ':' {
			pos++
			digits = 0
			for pos < len(s) && s[pos] >= '0' && s[pos] <= '9' {
				seconds = seconds*10 + int32(s[pos]-'0')
				pos++
				digits++
			}
			if digits == 0 {
				return 0, pos, false
			}
		}
	}
	return sign * (hours*3600 + minutes*60 + seconds), pos, true
}

// skipName advances over a quoted or alphabetic zone abbreviation.
func skipName(s string, pos int) (int, bool) {
	if pos >= len(s) {
		return pos, false
	}
	if s[pos] == '<' {
		pos++
		for pos < len(s) && s[pos] != '>' {
			pos++
		}
		if pos >= len(s) {
			return pos, false
		}
		return pos + 1, true
	}
	start := pos
	for pos < len(s) && ((s[pos] >= 'A' && s[pos] <= 'Z') || (s[pos] >= 'a' && s[pos] <= 'z')) {
		pos++
	}
	return pos, pos > start
}

// parseRuleDate reads one M, J, or numeric day rule and its optional time.
func parseRuleDate(s string) (ruleDate, bool) {
	r := ruleDate{second: 7200}
	pos := 0
	if pos >= len(s) {
		return r, false
	}
	switch s[pos] {
	case 'J':
		r.kind = 'J'
		pos++
		start := pos
		for pos < len(s) && s[pos] >= '0' && s[pos] <= '9' {
			pos++
		}
		if pos == start {
			return r, false
		}
		n, err := strconv.Atoi(s[start:pos])
		if err != nil || n < 1 || n > 365 {
			return r, false
		}
		r.day = n
	case 'M':
		r.kind = 'M'
		pos++
		part := func(max int) (int, bool) {
			start := pos
			for pos < len(s) && s[pos] >= '0' && s[pos] <= '9' {
				pos++
			}
			if pos == start {
				return 0, false
			}
			n, err := strconv.Atoi(s[start:pos])
			if err != nil || n < 0 || n > max {
				return 0, false
			}
			return n, true
		}
		var ok bool
		if r.month, ok = part(12); !ok || r.month < 1 {
			return r, false
		}
		if pos >= len(s) || s[pos] != '.' {
			return r, false
		}
		pos++
		if r.week, ok = part(5); !ok || r.week < 1 {
			return r, false
		}
		if pos >= len(s) || s[pos] != '.' {
			return r, false
		}
		pos++
		if r.weekday, ok = part(6); !ok {
			return r, false
		}
	default:
		r.kind = 'n'
		start := pos
		for pos < len(s) && s[pos] >= '0' && s[pos] <= '9' {
			pos++
		}
		if pos == start {
			return r, false
		}
		n, err := strconv.Atoi(s[start:pos])
		if err != nil || n > 365 {
			return r, false
		}
		r.day = n
	}
	if pos < len(s) && s[pos] == '/' {
		pos++
		secs, next, ok := parseOffset(s[pos:], 0)
		if !ok {
			return r, false
		}
		pos += next
		r.second = int64(secs)
	}
	return r, pos == len(s)
}

// parseTZString reads a POSIX TZ rule from a TZif footer. Offsets in the
// footer are positive west of Greenwich, so the stored values are negated.
func parseTZString(s string) (posixRule, bool) {
	var r posixRule
	pos, ok := skipName(s, 0)
	if !ok {
		return r, false
	}
	off, next, ok := parseOffset(s, pos)
	if !ok {
		return r, false
	}
	r.stdOff = -off
	pos = next
	if pos >= len(s) {
		return r, true
	}
	pos, ok = skipName(s, pos)
	if !ok {
		return r, false
	}
	r.hasDST = true
	r.dstOff = r.stdOff + 3600
	if pos < len(s) && s[pos] != ',' {
		off, next, ok = parseOffset(s, pos)
		if !ok {
			return r, false
		}
		r.dstOff = -off
		pos = next
	}
	if pos >= len(s) || s[pos] != ',' {
		return r, false
	}
	pos++
	comma := strings.IndexByte(s[pos:], ',')
	if comma < 0 {
		return r, false
	}
	start, ok := parseRuleDate(s[pos : pos+comma])
	if !ok {
		return r, false
	}
	end, ok := parseRuleDate(s[pos+comma+1:])
	if !ok {
		return r, false
	}
	r.start = start
	r.end = end
	return r, true
}

func isLeap(year int) bool { return year%4 == 0 && (year%100 != 0 || year%400 == 0) }

func daysInMonth(year, month int) int {
	switch month {
	case 4, 6, 9, 11:
		return 30
	case 2:
		if isLeap(year) {
			return 29
		}
		return 28
	default:
		return 31
	}
}

// daysFromCivil is Howard Hinnant's days_from_civil.
func daysFromCivil(year, month, day int) int64 {
	y := int64(year)
	if month <= 2 {
		y--
	}
	era := y / 400
	if y < 0 && y%400 != 0 {
		era--
	}
	yoe := y - era*400
	mp := int64(month)
	if month > 2 {
		mp -= 3
	} else {
		mp += 9
	}
	doy := (153*mp+2)/5 + int64(day) - 1
	doe := yoe*365 + yoe/4 - yoe/100 + doy
	return era*146097 + doe - 719468
}

// ruleUTC returns the UTC second of one rule in one year. off is the offset in
// effect before the transition, because the rule names a local wall time.
func ruleUTC(r ruleDate, year int, off int32) int64 {
	if r.kind == 'M' {
		first := daysFromCivil(year, r.month, 1)
		// 1970-01-01 was a Thursday; 0 is Sunday.
		weekday := int((first + 4) % 7)
		if weekday < 0 {
			weekday += 7
		}
		day := 1 + (r.weekday-weekday+7)%7 + (r.week-1)*7
		if day > daysInMonth(year, r.month) {
			day -= 7
		}
		return daysFromCivil(year, r.month, day)*86400 + r.second - int64(off)
	}
	day := r.day
	if r.kind == 'J' {
		day = r.day - 1
		if isLeap(year) && r.day >= 60 {
			day++
		}
	}
	return daysFromCivil(year, 1, 1)*86400 + int64(day)*86400 + r.second - int64(off)
}

// ruleTransitions lists the two transitions of a DST rule for every year.
func ruleTransitions(r posixRule, fromYear, toYear int) []transition {
	var out []transition
	for year := fromYear; year <= toYear; year++ {
		start := transition{at: ruleUTC(r.start, year, r.stdOff), offset: r.dstOff}
		end := transition{at: ruleUTC(r.end, year, r.dstOff), offset: r.stdOff}
		if start.at < end.at {
			out = append(out, start, end)
		} else {
			out = append(out, end, start)
		}
	}
	return out
}

// offsetAt looks up an offset in one zone's transition slice.
func offsetAt(trans []transition, initial int32, at int64) int32 {
	lo, hi := 0, len(trans)
	for lo < hi {
		mid := (lo + hi) / 2
		if trans[mid].at <= at {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo == 0 {
		return initial
	}
	return trans[lo-1].offset
}

func main() {
	goroot := os.Getenv("GOROOT")
	if goroot == "" {
		goroot = "C:/Program Files/Go"
	}
	r, err := zip.OpenReader(filepath.Join(goroot, "lib", "time", "zoneinfo.zip"))
	if err != nil {
		panic(err)
	}
	defer r.Close()
	type entry struct {
		name string
		data []byte
	}
	var entries []entry
	for _, f := range r.File {
		rc, err := f.Open()
		if err != nil {
			panic(err)
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			panic(err)
		}
		entries = append(entries, entry{name: f.Name, data: data})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].name < entries[j].name })
	var zones []zone
	var trans []transition
	for _, e := range entries {
		explicit, initial, footer, ok := parseTZif(e.data)
		if !ok {
			panic("parse " + e.name)
		}
		merged := explicit
		if len(explicit) > 0 && footer != "" {
			if rule, ok := parseTZString(footer); ok && rule.hasDST {
				last := explicit[len(explicit)-1]
				lastYear := time.Unix(last.at, 0).UTC().Year()
				for _, t := range ruleTransitions(rule, lastYear, 2270) {
					if t.at > last.at {
						merged = append(merged, t)
					}
				}
			}
		}
		loc, err := time.LoadLocation(e.name)
		if err != nil {
			panic("load " + e.name)
		}
		checks := []int64{tableStart, -2208988800, -1, 0, 1, 1000000000, 1700000000, 1710000000, tableEnd - 1, 7258118400}
		for _, t := range merged {
			checks = append(checks, t.at-1, t.at, t.at+1)
		}
		for year := 1900; year <= 2262; year += 50 {
			for _, month := range []time.Month{1, 4, 7, 10} {
				checks = append(checks, time.Date(year, month, 1, 0, 0, 0, 0, time.UTC).Unix())
			}
		}
		for _, at := range checks {
			if at < tableStart || at >= tableEnd {
				continue
			}
			_, want := time.Unix(at, 0).In(loc).Zone()
			got := int(offsetAt(merged, initial, at))
			if got != want {
				panic(fmt.Sprintf("verify %s at %d: got %d, want %d", e.name, at, got, want))
			}
		}
		first := len(trans)
		trans = append(trans, merged...)
		zones = append(zones, zone{name: e.name, initial: initial, first: first, count: len(merged)})
	}
	var b strings.Builder
	b.WriteString("/* Generated by std/time/tzgen from the Go zoneinfo IANA database. Do not edit. */\n\n")
	b.WriteString("#include \"zone.h\"\n\n")
	b.WriteString("static const tz_transition tz_transitions[] = {\n")
	for _, t := range trans {
		fmt.Fprintf(&b, "    {%dLL, %d},\n", t.at, t.offset)
	}
	b.WriteString("};\n\nstatic const tz_zone tz_zones[] = {\n")
	for _, z := range zones {
		fmt.Fprintf(&b, "    {\"%s\", %d, %d, %d},\n", z.name, z.initial, z.first, z.count)
	}
	b.WriteString("};\n\n")
	b.WriteString("const int32_t tz_zone_count = (int32_t)(sizeof(tz_zones) / sizeof(tz_zones[0]));\n")
	b.WriteString("const tz_zone* tz_zone_table = tz_zones;\n")
	b.WriteString("const tz_transition* tz_transition_table = tz_transitions;\n")
	if err := os.WriteFile(filepath.Join("std", "time", "tzdata.c"), []byte(b.String()), 0o644); err != nil {
		panic(err)
	}
	fmt.Printf("wrote std/time/tzdata.c: zones=%d transitions=%d\n", len(zones), len(trans))
}
