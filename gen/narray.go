// narray reads a directory of .periph XML files (one ST peripheral each,
// see periph/stm32g4/SCHEMA.md) and generates the bare-metal C support layer
// for an STM32 family: register-struct headers, the IRQ vector table, and the
// linker address symbols.
//
// Theory of operation: each .periph file is its own <device> root, so loading a
// family means parsing every file and merging the peripherals and interrupts
// into one Device. Peripherals form explicit inheritance chains via base=/
// extend=/exclude=; resolve() flattens each concrete peripheral into a complete,
// offset-sorted register list. Everything downstream (templates) consumes the
// resolved model and never re-derives structure.
//
// This is a fresh rewrite of the older stm32gen/genstruct, which parsed ST SVDs.
// The .periph data is richer (named field enums, errata, pinmux, explicit
// inheritance) so the superset-inference genstruct needed is gone.
package main

import (
	"encoding/json"
	"encoding/xml"
	"flag"
	"fmt"
	"log"
	"math/bits"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// ---- numbers --------------------------------------------------------------

// Hex parses the lowercase 0x-prefixed values used throughout .periph
// (offsets, masks, reset values, base addresses).
type Hex uint64

func (h *Hex) UnmarshalXMLAttr(attr xml.Attr) error {
	if attr.Value == "" {
		return nil
	}
	n, err := strconv.ParseUint(attr.Value, 0, 64)
	if err != nil {
		return fmt.Errorf("bad hex %q: %w", attr.Value, err)
	}
	*h = Hex(n)
	return nil
}

func (h Hex) Hex() string { return fmt.Sprintf("0x%X", uint64(h)) }

// ---- parsed schema --------------------------------------------------------
// These mirror the .periph XML exactly. Synthesized/derived data lives in the
// `json:"-"`-tagged fields lower down and is filled by resolve().

// file is one <device> root. Different .periph files populate different
// children: most carry <peripheral>s, INTERRUPTS.periph carries <interrupts>,
// MEMORY/ERRATA/PINMUX carry their own shapes (parsed later milestones).
type file struct {
	Name            string        `xml:"name,attr"`
	ReferenceManual string        `xml:"reference_manual,attr"`
	Revision        string        `xml:"revision,attr"`
	Peripherals     []*Peripheral `xml:"peripheral"`
	Interrupts      []*Interrupt  `xml:"interrupts>interrupt"`
	Pins            []*Pin        `xml:"pinmux>pin"`
}

// Pin is one GPIO pad and the alternate functions the datasheet maps onto it.
type Pin struct {
	Name string `xml:"name,attr"` // PA0..PG15
	AFs  []*AF  `xml:"af"`
}

type AF struct {
	Num  int    `xml:"num,attr"`
	Func string `xml:"func,attr"` // e.g. USART2_CTS; "-" means none
	Cat  string `xml:"cat,attr"`  // category restriction, e.g. "3,4"
	Note string `xml:"note,attr"`
}

type Peripheral struct {
	Name         string      `xml:"name,attr"`
	Description  string      `xml:"description,attr"`
	Abstract     bool        `xml:"abstract,attr"`
	Base         string      `xml:"base,attr"`         // parent peripheral name, "" if none
	ProdCategory string      `xml:"prodcategory,attr"` // "", "2", "3", "4"
	Instances    []*Instance `xml:"instance"`
	Registers    []*Register `xml:"register"`
	Groups       []*Group    `xml:"group"`

	// synthesized by resolve()
	Resolved []*Register `xml:"-"` // full, offset-sorted, inheritance flattened
}

type Instance struct {
	ID           string `xml:"id,attr"`   // "1".."20" or "A".."G"
	Name         string `xml:"name,attr"` // optional explicit symbol, overrides the derived stem+id
	Base         Hex    `xml:"base,attr"`
	ProdCategory string `xml:"prodcategory,attr"`
}

type Group struct {
	Name        string      `xml:"name,attr"`
	Offset      Hex         `xml:"offset,attr"`
	Stride      Hex         `xml:"stride,attr"`
	RangeMin    int         `xml:"range_min,attr"`
	RangeMax    int         `xml:"range_max,attr"`
	Description string      `xml:"description,attr"`
	Registers   []*Register `xml:"register"`
}

type Register struct {
	Name         string     `xml:"name,attr"`
	Offset       Hex        `xml:"offset,attr"`
	Reset        Hex        `xml:"reset,attr"`
	Description  string     `xml:"description,attr"`
	Access       string     `xml:"access,attr"` // default for fields: ro|wo|rw...
	Extend       bool       `xml:"extend,attr"` // merge into inherited register of same name
	ProdCategory string     `xml:"prodcategory,attr"`
	Fields       []*Field   `xml:"field"`
	Excludes     []*Exclude `xml:"exclude"`

	// synthesized from a <group>. ArrayLen>0 marks an array member:
	//   Sub==nil: an array of 32-bit registers (NVIC ISER[8]).
	//   Sub!=nil: an array of a sub-struct (DMA CH[8] of CR/NDTR/PAR/MAR),
	//             Stride bytes per element, Sub holding the body registers.
	// RangeMin is the .periph first index (1 for DMA channels) — used to name
	// the index enums that map a hardware index to the 0-based array slot.
	ArrayLen int         `xml:"-"`
	Stride   int         `xml:"-"`
	RangeMin int         `xml:"-"`
	Sub      []*Register `xml:"-"`
}

type Field struct {
	Name         string  `xml:"name,attr"`
	Mask         Hex     `xml:"mask,attr"`
	Access       string  `xml:"access,attr"`
	Description  string  `xml:"description,attr"`
	ProdCategory string  `xml:"prodcategory,attr"`
	Enums        []*Enum `xml:"enum"`
}

// Shift is the bit position of the field's least-significant bit.
func (f *Field) Shift() int { return bits.TrailingZeros64(uint64(f.Mask)) }

// Width is the bit count. For the non-contiguous masks .periph occasionally
// uses, this is the popcount; Shift+Width is only meaningful when contiguous.
func (f *Field) Width() int { return bits.OnesCount64(uint64(f.Mask)) }

// Contiguous reports whether the set bits form one run (mask>>shift is all-ones).
func (f *Field) Contiguous() bool {
	m := uint64(f.Mask) >> f.Shift()
	return m&(m+1) == 0
}

type Enum struct {
	Value       Hex    `xml:"value,attr"`
	Name        string `xml:"name,attr"`
	Description string `xml:"description,attr"`
}

type Exclude struct {
	Field string `xml:"field,attr"`
}

type Interrupt struct {
	IRQ         int    `xml:"irq,attr"`
	Name        string `xml:"name,attr"`
	Description string `xml:"description,attr"`
}

// ---- merged device --------------------------------------------------------

type Device struct {
	Name            string
	ReferenceManual string
	Revision        string
	Peripherals     []*Peripheral
	Interrupts      []*Interrupt
	Pins            []*Pin

	byName map[string][]*Peripheral // a name may have category variants (FLASH Cat2/3/4)
}

// peripheral resolves a name used as an inheritance base. Bases are unique
// abstract/concrete names; a name with category variants is never a base, so
// ambiguity there is reported rather than guessed.
func (d *Device) peripheral(name string) (*Peripheral, error) {
	ps := d.byName[name]
	switch len(ps) {
	case 0:
		return nil, fmt.Errorf("peripheral %q not found", name)
	case 1:
		return ps[0], nil
	default:
		return nil, fmt.Errorf("peripheral %q is ambiguous (%d category variants)", name, len(ps))
	}
}

// load parses every *.periph in dir and merges them into one Device.
func load(dir string) (*Device, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.periph"))
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("no .periph files in %s", dir)
	}
	sort.Strings(paths)

	d := &Device{byName: map[string][]*Peripheral{}}
	seenKey := map[string]string{} // name\x00prodcategory -> source file
	for _, p := range paths {
		buf, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		var f file
		if err := xml.Unmarshal(buf, &f); err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Base(p), err)
		}
		if d.Name == "" {
			d.Name, d.ReferenceManual, d.Revision = f.Name, f.ReferenceManual, f.Revision
		}
		for _, per := range f.Peripherals {
			key := per.Name + "\x00" + per.ProdCategory
			if prev, dup := seenKey[key]; dup {
				return nil, fmt.Errorf("%s: duplicate peripheral %s (prodcategory %q), first in %s",
					filepath.Base(p), per.Name, per.ProdCategory, prev)
			}
			seenKey[key] = filepath.Base(p)
			cleanws(&per.Description)
			d.byName[per.Name] = append(d.byName[per.Name], per)
			d.Peripherals = append(d.Peripherals, per)
		}
		d.Interrupts = append(d.Interrupts, f.Interrupts...)
		d.Pins = append(d.Pins, f.Pins...)
	}

	sort.Slice(d.Peripherals, func(i, j int) bool { return d.Peripherals[i].Name < d.Peripherals[j].Name })
	sort.Slice(d.Interrupts, func(i, j int) bool { return d.Interrupts[i].IRQ < d.Interrupts[j].IRQ })
	return d, nil
}

// ---- inheritance resolution ----------------------------------------------

// resolve flattens every concrete peripheral's inheritance chain into a
// complete, offset-sorted register list in Peripheral.Resolved. Abstract
// peripherals are templates and are left unresolved.
func (d *Device) resolve() error {
	for _, p := range d.Peripherals {
		if p.Abstract {
			continue
		}
		regs, err := d.flatten(p, map[string]bool{})
		if err != nil {
			return fmt.Errorf("%s: %w", p.Name, err)
		}
		sort.SliceStable(regs, func(i, j int) bool { return regs[i].Offset < regs[j].Offset })
		p.Resolved = regs
	}
	return nil
}

// flatten returns the register list for p with its base chain applied:
// base registers first, then this level adds new registers, extends merge
// fields into an inherited register, and excludes drop inherited fields.
// Returns deep-ish copies so peripherals sharing a base don't alias.
func (d *Device) flatten(p *Peripheral, seen map[string]bool) ([]*Register, error) {
	if seen[p.Name] {
		return nil, fmt.Errorf("inheritance cycle at %s", p.Name)
	}
	seen[p.Name] = true

	var base []*Register
	if p.Base != "" {
		parent, err := d.peripheral(p.Base)
		if err != nil {
			return nil, err
		}
		if base, err = d.flatten(parent, seen); err != nil {
			return nil, err
		}
	}

	// index inherited registers by name for extend/exclude
	out := make([]*Register, len(base))
	idx := map[string]int{}
	for i, r := range base {
		out[i] = r
		idx[r.Name] = i
	}

	// own registers, plus group registers expanded with their absolute offsets
	own := append([]*Register{}, p.Registers...)
	for _, g := range p.Groups {
		own = append(own, expandGroup(g))
	}

	for _, r := range own {
		if r.Extend {
			i, ok := idx[r.Name]
			if !ok {
				return nil, fmt.Errorf("extend of unknown register %s", r.Name)
			}
			merged := mergeRegister(out[i], r)
			out[i] = merged
			continue
		}
		if i, ok := idx[r.Name]; ok { // plain redefinition replaces
			out[i] = r
			continue
		}
		idx[r.Name] = len(out)
		out = append(out, r)
	}
	return out, nil
}

// mergeRegister returns base with the extending register applied: excluded
// fields removed, fields redefined by ext replaced (a derived peripheral
// narrows or widens an inherited field — e.g. FLASH CR.PNB, LPUART CR2.STOP),
// and remaining new fields appended. base is not mutated.
func mergeRegister(base, ext *Register) *Register {
	excluded := map[string]bool{}
	for _, e := range ext.Excludes {
		excluded[e.Field] = true
	}
	redefined := map[string]bool{}
	for _, f := range ext.Fields {
		redefined[f.Name] = true
	}
	m := *base
	m.Fields = nil
	for _, f := range base.Fields {
		if !excluded[f.Name] && !redefined[f.Name] {
			m.Fields = append(m.Fields, f)
		}
	}
	m.Fields = append(m.Fields, ext.Fields...)
	if ext.Description != "" {
		m.Description = ext.Description
	}
	return &m
}

// expandGroup turns a <group> (a register block repeated `stride` bytes apart)
// into a single array register at the group's offset:
//   - one unnamed register, stride == one register: a scalar array (NVIC
//     ISER[8]) — Sub stays nil.
//   - otherwise: a struct array (DMA CH[8]), the body registers carried in
//     Sub at their element-relative offsets.
func expandGroup(g *Group) *Register {
	count := g.RangeMax - g.RangeMin + 1

	// one register per element, packed: a scalar array (NVIC ISER[8], AES
	// KEYR[8]). The element register's own name is a placeholder; the array
	// takes the group name.
	if len(g.Registers) == 1 && int(g.Stride) == regBytes {
		r := *g.Registers[0]
		r.Name = g.Name
		r.Offset = g.Offset
		r.ArrayLen = count
		return &r
	}

	return &Register{
		Name:     g.Name,
		Offset:   g.Offset,
		ArrayLen: count,
		Stride:   int(g.Stride),
		RangeMin: g.RangeMin,
		Sub:      g.Registers,
	}
}

// ---- helpers --------------------------------------------------------------

func cleanws(s *string) { *s = strings.Join(strings.Fields(*s), " ") }

// ---- main -----------------------------------------------------------------

var (
	fDump   = flag.Bool("dump", false, "dump the resolved device model as JSON and exit")
	fHeader = flag.Bool("header", false, "emit the C register header to stdout")
	fDevs   = flag.Bool("devs", false, "emit the devs.ld peripheral-address linker script to stdout")
	fPinmux = flag.Bool("pinmux", false, "emit the pinmux.h GPIO pin-map header to stdout")
	fPinfmt = flag.String("pinfmt", "", "rewrite the //% pinout annotations in this C file in place")
	fPinout = flag.String("pinout", "", "emit a Markdown pinout table for this board C file to stdout")
)

func main() {
	log.SetFlags(0)
	log.SetPrefix("narray: ")
	flag.Parse()

	if len(flag.Args()) != 1 {
		log.Fatalf("usage: %s [-dump] path/to/periph/family", os.Args[0])
	}

	d, err := load(flag.Arg(0))
	if err != nil {
		log.Fatal(err)
	}
	if err := d.resolve(); err != nil {
		log.Fatal(err)
	}

	if *fDump {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(d); err != nil {
			log.Fatal(err)
		}
		return
	}

	if *fHeader {
		writeHeader(os.Stdout, d)
		return
	}

	if *fDevs {
		writeDevsLD(os.Stdout, d)
		return
	}

	if *fPinmux {
		writePinmux(os.Stdout, d)
		return
	}

	if *fPinfmt != "" {
		if err := pinfmtFile(d, *fPinfmt); err != nil {
			log.Fatal(err)
		}
		return
	}

	if *fPinout != "" {
		if err := pinoutMarkdown(d, *fPinout, os.Stdout); err != nil {
			log.Fatal(err)
		}
		return
	}

	// summary until templates land
	var abstract, concrete, instances int
	for _, p := range d.Peripherals {
		if p.Abstract {
			abstract++
			continue
		}
		concrete++
		instances += len(p.Instances)
	}
	log.Printf("device %s (%s rev %s): %d peripherals (%d concrete, %d abstract), %d instances, %d interrupts",
		d.Name, d.ReferenceManual, d.Revision, len(d.Peripherals), concrete, abstract, instances, len(d.Interrupts))
}
