package main

// Header emission. Turns the resolved Device model into the C register header:
// the IRQ enum, one `struct <Periph>_Type` per concrete peripheral with
// registers at correct offsets (RESERVED gap fill, unions for same-offset
// registers), the per-register field-mask enums, named field-value enums (the
// .periph win genstruct couldn't do), and `static inline` get/set accessors.
//
// Accessors follow genstruct's rule: when a peripheral has a single instance it
// is a singleton — accessors take no pointer and address the global directly
// (cordic_csr_set_func(v)); otherwise they take a `struct <Periph>_Type* p`.
//
// This is hand-written Go rather than a text/template: the layout logic
// (gaps, unions, singleton branching) reads far clearer as code than as
// nested template actions.

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode"
)

const regBytes = 4 // .periph registers are 32-bit; 16-bit peripherals (USB) are a later concern

// stem strips a peripheral name down to the prefix shared by its instances:
// cut at the first '_', then drop trailing digits.
//
//	USART      -> USART     (USART1, USART2)
//	TIM_GP32   -> TIM       (TIM2, TIM5)
//	TIM16_17   -> TIM       (TIM16, TIM17)
//	GPIO       -> GPIO      (GPIOA, GPIOB)
func stem(name string) string {
	if i := strings.IndexByte(name, '_'); i >= 0 {
		name = name[:i]
	}
	return strings.TrimRightFunc(name, unicode.IsDigit)
}

// symbol is the linker/global name for one instance.
func (in *Instance) symbol(p *Peripheral) string {
	switch {
	case in.Name != "": // explicit override (e.g. LPUART1)
		return in.Name
	case len(p.Instances) == 1: // single instance keeps the full descriptive name
		return p.Name
	default:
		return stem(p.Name) + in.ID
	}
}

func (p *Peripheral) typeName() string { return p.Name + "_Type" }

// catInstances are the instances present in the selected category.
func (p *Peripheral) catInstances() []*Instance {
	var out []*Instance
	for _, in := range p.Instances {
		if inCat(in.ProdCategory) {
			out = append(out, in)
		}
	}
	return out
}

// singleton: one instance in this category ⇒ accessors drop the pointer arg.
func (p *Peripheral) singleton() bool { return len(p.catInstances()) == 1 }

// emitPeripherals returns the concrete peripherals to emit, one struct per
// name. With a part selected (-part), inCat keeps the single matching category
// variant (FLASH Cat2/3/4); with no part, variants collapse to the highest
// category — the most complete choice for a whole-family header.
func (d *Device) emitPeripherals() []*Peripheral {
	best := map[string]*Peripheral{}
	for _, p := range d.Peripherals {
		if p.Abstract || len(p.Resolved) == 0 || !inCat(p.ProdCategory) {
			continue
		}
		// With a part selected, inCat leaves one variant per name; otherwise
		// (whole family) keep the highest category as the most complete.
		if cur, ok := best[p.Name]; !ok || p.ProdCategory > cur.ProdCategory {
			best[p.Name] = p
		}
	}
	out := make([]*Peripheral, 0, len(best))
	for _, p := range best {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// regSpan is the byte size a register occupies in its struct: one word, or for
// array members the element size times the count.
func regSpan(r *Register) uint64 {
	if r.ArrayLen == 0 {
		return regBytes
	}
	elem := regBytes
	if r.Sub != nil {
		elem = r.Stride
	}
	return uint64(elem) * uint64(r.ArrayLen)
}

// member is one line of a struct: a run of registers sharing an offset (1 =
// plain field, >1 = union) or a reserved gap of Bytes.
type member struct {
	Reserved bool
	Bytes    int
	Regs     []*Register
	Offset   uint64
}

// members lays out the resolved registers into struct members, inserting
// reserved gaps and folding same-offset registers into unions.
func (p *Peripheral) members() []member {
	var out []member
	var cursor uint64
	regs := p.Resolved

	for i := 0; i < len(regs); {
		if !inCat(regs[i].ProdCategory) {
			i++
			continue
		}
		off := uint64(regs[i].Offset)
		if off > cursor {
			out = append(out, member{Reserved: true, Bytes: int(off - cursor), Offset: cursor})
			cursor = off
		}
		// collect the run of registers at this same offset
		j := i
		for j < len(regs) && uint64(regs[j].Offset) == off {
			j++
		}
		out = append(out, member{Regs: regs[i:j:j], Offset: off})
		cursor = off + regSpan(regs[i])
		i = j
	}
	return out
}

// ---- C identifier fragments ----------------------------------------------

func cAccess(a string) string {
	switch a {
	case "ro":
		return "__I"
	case "wo":
		return "__O"
	default: // rw and the clear/set/toggle variants are all read/write storage
		return "__IO"
	}
}

// emittable reports whether a field is a sub-register slice worth a mask enum
// and accessors (a full-width 32-bit field is just the register itself).
func (f *Field) emittable() bool { return f.Width() < 32 }

func upper(s string) string { return strings.ToUpper(s) }
func lower(s string) string { return strings.ToLower(s) }

// ---- writer ---------------------------------------------------------------

func writeHeader(w io.Writer, d *Device) {
	p := func(format string, a ...any) { fmt.Fprintf(w, format, a...) }

	p("#pragma once\n\n")
	p("// Register definitions for %s (%s rev %s).\n", d.Name, d.ReferenceManual, d.Revision)
	p("// Generated by narray from .periph files, DO NOT EDIT.\n\n")
	p("#include <stdint.h>\n\n")
	// Sentinel macro so headers like nvic.h can require a device header first.
	// (The constants below are enums, invisible to the preprocessor.)
	p("#define NARRAY_DEVICE %s\n\n", d.Name)

	writeIRQ(w, d)

	// Vector-table sizing. The program owns the table literal (the manifest);
	// the generator only supplies its length. Slots 0..15 are the Cortex core
	// exceptions (SP, Reset, NMI, faults, SVCall, PendSV, SysTick) and are
	// positional — they never go through the device-IRQ +16 arithmetic. Device
	// IRQ n lives at slot n+16.
	mi := maxIRQ(d)
	slots := mi + 17
	p("\nenum { NVIC_VECTORS = %d }; // 16 core slots + %d device IRQs\n", slots, mi+1)
	// VTOR requires the table aligned to a power of two >= its byte size (and
	// >= 128). A RAM-relocated table must be declared with this alignment.
	p("enum { NVIC_VTOR_ALIGN = %d }; // bytes; align a RAM vector table to this\n", vtorAlign(slots))

	p("\n#define __I  volatile const  // read-only\n")
	p("#define __O  volatile        // write-only\n")
	p("#define __IO volatile        // read/write\n\n")

	for _, per := range d.emitPeripherals() {
		writePeripheral(w, per)
	}

	p("#undef __I\n#undef __O\n#undef __IO\n")
}

// vtorAlign is the byte alignment a relocated vector table needs: the smallest
// power of two >= the table's byte size, and >= 128 (VTOR bits [6:0] are zero).
func vtorAlign(slots int) int {
	a := 128
	for a < slots*regBytes {
		a <<= 1
	}
	return a
}

// maxIRQ is the highest device interrupt number (IRQn >= 0). Slots 0..15 are
// the Cortex core exceptions; device IRQ n lives at slot n+16.
func maxIRQ(d *Device) int {
	m := -1
	for _, in := range d.Interrupts {
		if in.IRQ > m {
			m = in.IRQ
		}
	}
	return m
}

// writeDevsLD emits the linker symbols that place each peripheral instance at
// its absolute base address. Paired with the header's `extern struct T sym;`
// declarations, this lets the compiler address registers as compile-time
// constants — no runtime base pointer. Symbols and addresses are derived the
// same way as the header, so the two artifacts agree by construction.
func writeDevsLD(w io.Writer, d *Device) {
	p := func(format string, a ...any) { fmt.Fprintf(w, format, a...) }

	type sym struct {
		name string
		addr uint64
		typ  string
	}
	var syms []sym
	for _, per := range d.emitPeripherals() {
		for _, in := range per.Instances {
			if !inCat(in.ProdCategory) {
				continue
			}
			syms = append(syms, sym{in.symbol(per), uint64(in.Base), per.typeName()})
		}
	}
	sort.SliceStable(syms, func(i, j int) bool { return syms[i].addr < syms[j].addr })

	p("/* Peripheral instance addresses for %s (%s rev %s).\n", d.Name, d.ReferenceManual, d.Revision)
	p("   Generated by narray from .periph files, DO NOT EDIT. */\n\n")
	for _, s := range syms {
		p("%-20s = 0x%08X; /* struct %s */\n", s.name, s.addr, s.typ)
	}
	// GPIO_ALL: the lib/gpio.h port-overlay array, bound to the first GPIO port.
	if gp, err := d.peripheral("GPIO"); err == nil && len(gp.Instances) > 0 {
		p("\n%-20s = 0x%08X; /* lib/gpio.h port overlay */\n", "GPIO_ALL", uint64(gp.Instances[0].Base))
	}
}

func writeIRQ(w io.Writer, d *Device) {
	p := func(format string, a ...any) { fmt.Fprintf(w, format, a...) }
	p("enum IRQn_Type {\n")
	p("\tNone_IRQn  = -16, // estack / initial SP slot\n")
	p("\tReset_IRQn = -15, // reset, not a real IRQ\n")
	for _, in := range d.Interrupts {
		if in.Description != "" {
			p("\t%s_IRQn = %d, // %s\n", in.Name, in.IRQ, in.Description)
		} else {
			p("\t%s_IRQn = %d,\n", in.Name, in.IRQ)
		}
	}
	p("};\n")
}

func writePeripheral(w io.Writer, per *Peripheral) {
	p := func(format string, a ...any) { fmt.Fprintf(w, format, a...) }

	p("\n")
	if per.Description != "" {
		p("/* %s%s */\n", per.Description, singletonNote(per))
	}
	p("struct %s {\n", per.typeName())
	resv := 0
	for _, m := range per.members() {
		if m.Reserved {
			p("\tuint8_t RESERVED%d[%d]; // @0x%X\n", resv, m.Bytes, m.Offset)
			resv++
			continue
		}
		if len(m.Regs) == 1 {
			r := m.Regs[0]
			if r.Sub != nil { // struct array (DMA CH[8])
				writeStructArray(w, r)
				continue
			}
			if r.ArrayLen > 0 { // scalar array (NVIC ISER[8])
				p("\t%s uint32_t %s[%d]; // @0x%X %s\n", cAccess(r.Access), r.Name, r.ArrayLen, m.Offset, r.Description)
				continue
			}
			p("\t%s uint32_t %s; // @0x%X %s\n", cAccess(r.Access), r.Name, m.Offset, r.Description)
			continue
		}
		p("\tunion { // @0x%X\n", m.Offset)
		for _, r := range m.Regs {
			p("\t\t%s uint32_t %s; // %s\n", cAccess(r.Access), r.Name, r.Description)
		}
		p("\t};\n")
	}
	p("};\n")

	for _, in := range per.Instances {
		if !inCat(in.ProdCategory) {
			continue
		}
		p("extern struct %s %s; // @%s\n", per.typeName(), in.symbol(per), in.Base.Hex())
	}

	for _, m := range per.members() {
		if m.Reserved {
			continue
		}
		for _, r := range m.Regs {
			writeRegisterAPI(w, per, r)
		}
	}
}

func singletonNote(per *Peripheral) string {
	if per.singleton() {
		return fmt.Sprintf("\nOnly one instance: %s.", per.catInstances()[0].symbol(per))
	}
	return ""
}

// writeStructArray emits a nested sub-struct array member (DMA CH[8]): the body
// registers laid out at their element-relative offsets, padded to the stride.
func writeStructArray(w io.Writer, r *Register) {
	p := func(format string, a ...any) { fmt.Fprintf(w, format, a...) }

	sub := append([]*Register{}, r.Sub...)
	sort.SliceStable(sub, func(i, j int) bool { return sub[i].Offset < sub[j].Offset })

	p("\tstruct { // @0x%X x%d, stride 0x%X\n", uint64(r.Offset), r.ArrayLen, r.Stride)
	var cursor uint64
	resv := 0
	for _, s := range sub {
		off := uint64(s.Offset)
		if off > cursor {
			p("\t\tuint8_t RESERVED%d[%d];\n", resv, off-cursor)
			resv++
			cursor = off
		}
		p("\t\t%s uint32_t %s; // %s\n", cAccess(s.Access), s.Name, s.Description)
		cursor = off + regBytes
	}
	if uint64(r.Stride) > cursor {
		p("\t\tuint8_t RESERVED%d[%d];\n", resv, uint64(r.Stride)-cursor)
	}
	p("\t} %s[%d];\n", r.Name, r.ArrayLen)
}

// emittableFields returns the sub-register-slice fields worth a mask enum.
func emittableFields(r *Register) []*Field {
	var out []*Field
	for _, f := range r.Fields {
		if f.emittable() && inCat(f.ProdCategory) {
			out = append(out, f)
		}
	}
	return out
}

// writeFieldEnums emits a register's field-mask enum and named field-value
// enums under the given identifier prefix (PERIPH_REG). No accessors.
func writeFieldEnums(w io.Writer, prefix, owner, desc string, fields []*Field) {
	p := func(format string, a ...any) { fmt.Fprintf(w, format, a...) }

	p("\n// %s %s\n", owner, desc)
	p("enum {\n")
	for _, f := range fields {
		p("\t%s_%s = %s,", prefix, upper(f.Name), f.Mask.Hex())
		if f.Description != "" {
			p(" // %s", f.Description)
		}
		p("\n")
	}
	p("};\n")

	for _, f := range fields {
		if len(f.Enums) == 0 {
			continue
		}
		p("enum %s_%s {\n", prefix, upper(f.Name))
		for _, e := range f.Enums {
			p("\t%s_%s_%s = %s,", prefix, upper(f.Name), e.Name, e.Value.Hex())
			if e.Description != "" {
				p(" // %s", e.Description)
			}
			p("\n")
		}
		p("};\n")
	}
}

// writeRegisterAPI emits the field-mask enum, any named field-value enums, and
// the get/set accessors for one register.
func writeRegisterAPI(w io.Writer, per *Peripheral, r *Register) {
	p := func(format string, a ...any) { fmt.Fprintf(w, format, a...) }

	// struct-array group (DMA CH[8]): emit index enums mapping the hardware
	// index to the 0-based array slot, then the body registers' field enums.
	// No accessors — write CH[DMA_CH1].CR directly with the mask enums.
	if r.Sub != nil {
		p("\n// %s->%s[] index — hardware index N is array slot N-%d.\n", per.Name, r.Name, r.RangeMin)
		p("// WARNING: %s[] is 0-based; index by these names, not raw integers.\n", r.Name)
		p("enum {\n")
		for i := 0; i < r.ArrayLen; i++ {
			p("\t%s_%s%d = %d,\n", upper(per.Name), upper(r.Name), r.RangeMin+i, i)
		}
		p("};\n")
		for _, s := range r.Sub {
			if f := emittableFields(s); len(f) > 0 {
				// prefix includes the group name: a peripheral can have two
				// arrays whose bodies share a register name (FMC BTR.TR, BWR.TR).
				writeFieldEnums(w, upper(per.Name)+"_"+upper(r.Name)+"_"+upper(s.Name),
					per.Name+"->"+r.Name+"[]."+s.Name, s.Description, f)
			}
		}
		return
	}

	if r.ArrayLen > 0 {
		return // scalar array (NVIC ISER): full-width fields, nothing to emit
	}

	emit := emittableFields(r)
	if len(emit) == 0 {
		return
	}

	pfx := upper(per.Name) + "_" + upper(r.Name)
	writeFieldEnums(w, pfx, per.Name+"->"+r.Name, r.Description, emit)

	// accessors. Single-bit fields are used directly via their mask; multi-bit
	// fields get shift/mask get & set helpers. Read-only registers get no set.
	single := per.singleton()
	sym := ""
	if single {
		sym = per.catInstances()[0].symbol(per)
	}
	ref := func() string { // how an accessor reaches the register
		if single {
			return sym + "." + r.Name
		}
		return "p->" + r.Name
	}
	param := func() string {
		if single {
			return "void"
		}
		return "struct " + per.typeName() + "* p"
	}
	arg := func() string {
		if single {
			return ""
		}
		return "struct " + per.typeName() + "* p, "
	}

	for _, f := range emit {
		if !f.Contiguous() || f.Width() == 1 {
			continue // single-bit and exotic masks use the mask constant directly
		}
		fn := lower(per.Name) + "_" + lower(r.Name) + "_" + lower(f.Name)
		mask := pfx + "_" + upper(f.Name)
		sh := f.Shift()
		if r.Access != "ro" {
			p("static inline void %s_set(%suint32_t val) { %s = (%s & ~%s) | ((val<<%d) & %s); }\n",
				fn, arg(), ref(), ref(), mask, sh, mask)
		}
		p("static inline uint32_t %s_get(%s) { return (%s & %s) >> %d; }\n",
			fn, param(), ref(), mask, sh)
	}
}
