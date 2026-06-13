package main

// pinfmt — a gofmt for board pinouts. The C board[] table is the source of
// truth (compiler- and datasheet-checked); this rewrites the structured
// comment on each annotated pin line so the C itself reads as the pinout table,
// with the derived columns (pin, function, AF, attributes) kept fresh and
// aligned. Free-form human text after a " | " separator is preserved verbatim.
//
// A line opts in by carrying a "//%" marker. Everything left of "//%" (the
// pinconf_t expression) is the source narray reads; everything between "//%"
// and the first " | " is narray-owned and regenerated; everything after the
// first " | " is yours.
//
//	PA2_USART2_TX | PIN_HIGH,   //% PA2  USART2_TX  AF7  af,hi  | Serial A TX
//
// Resolution is name-based: each token is one of the constants narray emits
// into pinmux.h (a GPIO_Mux pin+function, a GPIO_Pin selector, or a PIN_ flag),
// so there is no numeric decoding to get wrong.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const pinfmtMark = "//%"
const pinfmtSep = " | " // separates narray-owned columns from human text

// symbols is narray's view of the constants it generates, used to decode a
// board expression back into columns.
type symbols struct {
	mux  map[string]muxInfo // PA9_USART1_TX -> {PA9, USART1_TX, 7}
	pin  map[string]bool    // PA9, PAAll, ... (pin selectors, no function)
	flag map[string]flagInfo
}

type muxInfo struct {
	pin string
	fn  string
	af  int
}

type flagInfo struct {
	mode  string // in/out/af/analog, "" if not a mode flag
	speed string // lo/med/fast/hi
	pull  string // pu/pd
	od    bool
}

func buildSymbols(d *Device) symbols {
	s := symbols{mux: map[string]muxInfo{}, pin: map[string]bool{}, flag: map[string]flagInfo{}}

	present := map[string]bool{}
	for _, id := range gpioPorts(d) {
		present[id] = true
		for n := 0; n < 16; n++ {
			s.pin[fmt.Sprintf("P%s%d", id, n)] = true
		}
		s.pin["P"+id+"All"] = true
		s.pin["GPIO_"+id] = true
	}

	for _, p := range d.Pins {
		port := strings.TrimPrefix(p.Name, "P")
		if len(port) == 0 || !present[port[:1]] {
			continue
		}
		seen := map[string]bool{}
		for _, af := range p.AFs {
			fn := cIdent(af.Func)
			if fn == "" || af.Func == "-" || seen[fn] {
				continue
			}
			seen[fn] = true
			s.mux[p.Name+"_"+fn] = muxInfo{pin: p.Name, fn: fn, af: af.Num}
		}
	}

	s.flag = map[string]flagInfo{
		"PIN_INPUT": {mode: "in"}, "PIN_OUTPUT": {mode: "out"}, "PIN_ANALOG": {mode: "analog"},
		"PIN_OPENDRAIN": {od: true}, "PIN_PULLUP": {pull: "pu"}, "PIN_PULLDOWN": {pull: "pd"},
		"PIN_LOW": {speed: "lo"}, "PIN_MEDIUM": {speed: "med"}, "PIN_FAST": {speed: "fast"}, "PIN_HIGH": {speed: "hi"},
	}
	return s
}

// columns are the derived fields shown for one pin line.
type columns struct {
	pin, fn, af, attrs string
	ok                 bool // false if an unknown token was seen
}

// decode turns a board expression ("PA2_USART2_TX | PIN_HIGH") into columns.
func (s symbols) decode(expr string) columns {
	var pins []string
	fn, af := "", -1
	mode, speed, pull, od := "", "", "", false
	ok := true

	for _, tok := range strings.Split(expr, "|") {
		tok = strings.Trim(strings.TrimSpace(tok), "()")
		switch {
		case tok == "":
			// nothing
		case s.mux[tok] != muxInfo{}:
			m := s.mux[tok]
			pins = append(pins, m.pin)
			fn, af, mode = m.fn, m.af, "af"
		case s.pin[tok]:
			pins = append(pins, tok)
		case s.flag[tok] != flagInfo{}:
			f := s.flag[tok]
			if f.mode != "" {
				mode = f.mode
			}
			if f.speed != "" {
				speed = f.speed
			}
			if f.pull != "" {
				pull = f.pull
			}
			if f.od {
				od = true
			}
		default:
			ok = false
		}
	}

	if mode == "" {
		mode = "in"
	}
	afStr := "-"
	if af >= 0 {
		afStr = fmt.Sprintf("AF%d", af)
	}
	funcStr := fn
	if funcStr == "" {
		funcStr = "GPIO"
	}
	attrs := []string{mode}
	if speed != "" && speed != "lo" {
		attrs = append(attrs, speed)
	}
	if pull != "" {
		attrs = append(attrs, pull)
	}
	if od {
		attrs = append(attrs, "od")
	}
	return columns{pin: strings.Join(pins, "|"), fn: funcStr, af: afStr, attrs: strings.Join(attrs, ","), ok: ok}
}

// boardEntry is one decoded pin line of a board[] table.
type boardEntry struct {
	idx         int    // line number in the file
	code, human string // pinconf expression (before //%) and preserved free-form text
	cols        columns
}

// parseBoard finds the //%-annotated pin lines in a board file and decodes each.
func parseBoard(syms symbols, lines []string) (entries []boardEntry, unknown int) {
	for i, ln := range lines {
		m := strings.Index(ln, pinfmtMark)
		if m < 0 {
			continue
		}
		code := strings.TrimRight(ln[:m], " \t")
		if strings.Contains(code, "//") {
			continue // the //% is inside an existing comment (prose), not a pin line
		}
		rest := ln[m+len(pinfmtMark):]
		human := ""
		if j := strings.Index(rest, pinfmtSep); j >= 0 {
			human = rest[j+len(pinfmtSep):]
		} else if h := strings.TrimSpace(rest); h != "" {
			human = h // first run: existing text is all human
		}
		cols := syms.decode(strings.TrimRight(strings.TrimSpace(code), ","))
		if cols.pin == "" {
			continue // no recognizable pin selector — not a pin line
		}
		if !cols.ok {
			unknown++
		}
		entries = append(entries, boardEntry{idx: i, code: code, human: strings.TrimSpace(human), cols: cols})
	}
	return entries, unknown
}

// pinfmtFile rewrites the //% annotations in path in place.
func pinfmtFile(d *Device, path string) error {
	buf, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.Split(string(buf), "\n")
	entries, unknown := parseBoard(buildSymbols(d), lines)

	var wCode, wPin, wFn, wAf, wAttrs int
	for _, e := range entries {
		wCode = max(wCode, len(e.code))
		wPin, wFn, wAf = max(wPin, len(e.cols.pin)), max(wFn, len(e.cols.fn)), max(wAf, len(e.cols.af))
		wAttrs = max(wAttrs, len(e.cols.attrs))
	}

	// rewrite aligned — code column, then derived columns, then human.
	changed := 0
	for _, e := range entries {
		attrs := e.cols.attrs
		if e.human != "" {
			attrs = fmt.Sprintf("%-*s", wAttrs, attrs) // pad so the human column aligns
		}
		col := fmt.Sprintf("%-*s  %-*s  %-*s  %s", wPin, e.cols.pin, wFn, e.cols.fn, wAf, e.cols.af, attrs)
		out := fmt.Sprintf("%-*s  %s %s", wCode, e.code, pinfmtMark, col)
		if e.human != "" {
			out += pinfmtSep + e.human
		}
		out = strings.TrimRight(out, " ")
		if out != lines[e.idx] {
			changed++
		}
		lines[e.idx] = out
	}

	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0644); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "narray: pinfmt %s: %d pins, %d rewritten", path, len(entries), changed)
	if unknown > 0 {
		fmt.Fprintf(os.Stderr, ", %d with unrecognized tokens", unknown)
	}
	fmt.Fprintln(os.Stderr)
	return nil
}

// pinoutMarkdown reads a board file and writes a Markdown pinout table — a
// generated view of the same source of truth, for documentation.
func pinoutMarkdown(d *Device, path string, w io.Writer) error {
	buf, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	entries, _ := parseBoard(buildSymbols(d), strings.Split(string(buf), "\n"))

	p := func(format string, a ...any) { fmt.Fprintf(w, format, a...) }
	p("# %s pinout\n\n", d.Name)
	p("Generated by narray from `%s` — edit the C, not this file.\n\n", filepath.Base(path))
	p("| Pin | Function | AF | Config | Connected to |\n")
	p("| --- | -------- | -- | ------ | ------------ |\n")
	for _, e := range entries {
		pin := strings.ReplaceAll(e.cols.pin, "|", ", ") // '|' would break the table cell
		af := e.cols.af
		if af == "-" {
			af = "" // empty cell reads cleaner than a dash
		}
		p("| %s | %s | %s | %s | %s |\n", pin, e.cols.fn, af, e.cols.attrs, e.human)
	}
	return nil
}
