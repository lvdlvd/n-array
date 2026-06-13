# [N]Array — design & roadmap

Status: design accepted, generator (M1) not yet started.
Audience: future me, and Claude sessions continuing this work.

## Why this exists

Over years I've built a bare-metal STM32 environment by copying and
refining the same substrate (`boot.c`, `clock`, `gpio2`, `fault`,
`tprintf`, `fifo`, `vectors.c`, a `.ld`, a device register header) from
one project to the next: b3dsm, bminator10, compinator, flightrecorder,
stm32l432_hello, and the stm32gen family. `stm32gen/genstruct` was the
first attempt to generalize the *register header* part: it parses an ST
SVD and emits clean C — `struct USART1_Type` at the right offsets,
bitmask `enum`s, `static inline` get/set accessors, plus `devs.ld` and
`vectors.c`. It still serves, but two things hold it back:

1. **It eats SVD.** ST's SVDs are buggy and lossy. To get usable output
   genstruct patches them at runtime and still can't recover named field
   values, errata, or pin alternate-function tables.
2. **The drivers are still copied by hand** and have diverged across
   projects.

[N]Array is the better environment. The key realization: we already
paid to hand-curate something far richer than SVD — the `.periph` files.

## The three existing assets (and the gap)

- **Data — `.periph` XML** (`stm32g4/`, ~43 files, complete for G4).
  Richer than SVD: **named enum values** per field, errata + workarounds,
  pinmux/AF tables, part-number decoder, RAM elements (FDCAN msg RAM, USB
  PMA), product-category gating (Cat2/3/4), and *explicit* inheritance
  (`base=`/`extend=`/`exclude=`). Schema: `stm32g4/SCHEMA.md`.
- **Generator — `stm32gen/genstruct`** (Go). Produces exactly the output
  style we want, but reads SVD and re-derives (via `isSuperset`)
  inheritance that `.periph` already states.
- **Library — ~8 drivers** copy-pasted across 7 projects, now divergent.

Gap: n-array has the good data but **no generator and no library yet**.
M1 closes the first half.

## Architecture — four layers

```
  .periph (family data)   ─┐
  board.def (part+pinout)  ─┼─►  narray gen  ─►  device.h, devs.ld, vectors.c,
  family memory/clock      ─┘                    pinmux.h, errata.h
                                                       │
  driver library (gpio/clock/uart/can/...) ───────────┼─►  project skeleton
  project template (Makefile, arm_cmX.h, .ld) ────────┘     (ready to build)
```

- **L1 Data** — `.periph` per family. Family-level: all instances and
  categories. G4 done; L4/G0/H7 later.
- **L2 Board** — small per-*project* definition: the concrete part
  (e.g. G431RB → flash/RAM size, prodcategory, package), the clock-tree
  target, and the pinout (which AF on which pin). This is what genstruct
  never had — it turns family data into one chip.
- **L3 Generator** — `narray`, consuming L1(+L2) → the artifacts
  genstruct emits, plus what `.periph` newly enables.
- **L4 Library + scaffold** — the drivers, generalized only where the
  rule-of-3-5 is already paid (gpio/clock/boot/fault/tprintf/fifo: 5–7×),
  plus the project template.

## Decisions (locked)

- **Generator: Go, fresh rewrite.** New program designed around the
  `.periph` schema (enums, groups, errata, ram_elements, prodcategory),
  borrowing genstruct's `text/template` output templates. Not a port —
  the data model differs enough (inheritance is explicit, so the
  `isSuperset` machinery is deleted, not carried).
- **`.periph` is ground truth. SVD is retired.** We already paid to
  curate `.periph`; don't reintroduce buggy SVD.
- **Generated code is checked in.** Regeneration is opt-in by deleting
  the target (as stm32gen Makefiles already do). Toolchain stays just
  `arm-none-eabi-gcc`.
- **Library generalized lazily.** The 5–7×-repeated drivers get
  consolidated; CAN/USB/clock-variants stay copies until the right
  interface is obvious (per style.md rule-of-3-5).

## Output style to preserve

From genstruct, keep exactly:
- `struct XXX_Type` with registers at correct offsets, `RESERVED` gap
  fill, `union` for same-offset registers, `__I/__O/__IO` qualifiers.
- Singleton elision: one instance ⇒ drop the `struct* p` argument from
  accessors and address the global directly (`CORDIC.CSR`). (Done.)
- Type name = peripheral name (`struct USART_Type`, globals USART1/2/3),
  cleaner than genstruct's `USART1_Type`. Instance symbol = full name
  when singleton, else stem+id (`TIM2`, `GPIOA`); `name=` override for
  the lone exception (`LPUART1`). `_as_` casts between inheritance-
  related types deferred.
- Bitmask `enum`s `PERIPH_REG_FIELD = ...` and
  `periph_reg_set_field()/get_field()` inlines.
- `enum IRQn_Type`, `devs.ld` address symbols. (The weak-aliased
  `vectors.c` table genstruct emitted is **dropped** — see "Interrupt
  vectoring" below.)

New, enabled by `.periph`:
- **Named field-value enums** from `<enum>` (`enum CORDIC_CSR_FUNC {
  CORDIC_CSR_FUNC_Sine = ... }`), debugger-visible, the style.md ideal.
- **Register arrays** from `<group>` (implemented):
  - one register per element → scalar array (`NVIC ISER[8]`, `AES KEYR[8]`).
  - several registers per element → nested sub-struct array with stride
    padding (`DMA CH[8]` of CR/NDTR/PAR/MAR), plus generated 0-based
    **index enums** (`DMA_CH1 = 0` …) so hardware-numbered channels are
    indexed by name, never raw integers (a warning says so). Body field
    enums are prefixed with the group name (`DMA_CH_CR_EN`,
    `FMC_BTR_TR_*` vs `FMC_BWR_TR_*`) to stay collision-free.
- **prodcategory filtering** driven by the L2 board selection.
- **errata.h** / inline comments near affected registers from
  `ERRATA.periph`.
- **pinmux.h** validated AF helpers from `PINMUX.periph`.

## Interrupt vectoring & the NVIC core

Decided in design discussion; replaces genstruct's weak-alias scheme.

**The vector table is the program manifest.** Instead of a generated
`vectors.c` with 100 weak `*_Handler` aliases defaulting to a shared
spin-loop, the *application* owns a single designated-initializer array.
You name handlers whatever you like and place them by the generated
`IRQn_Type` enum:

```c
const isr_t __vectors[NVIC_VECTORS] __attribute__((section(".isr_vector"))) = {
    (isr_t)&_estack,                    // [0] SP    — core, positional
    Reset_Handler,                      // [1] Reset — core, positional, NO +16
    [3]                   = fault,      // [3] HardFault — core slot
    [VECTOR(USART1_IRQn)] = usart1_isr, //     device IRQ via +16
    [VECTOR(TIM2_IRQn)]   = tim2_isr,
};
```

Key facts (verified on arm-none-eabi-gcc cortex-m4):
- **Slots 0..15 are the Cortex core exceptions** (SP, Reset, NMI, the
  faults, SVCall, PendSV, SysTick) and are *positional*. The reset
  handler is slot 1 and does **not** go through the `+16` arithmetic.
- **Device IRQ n is at slot n+16.** `VECTOR(irq) ((irq)+16)` is for
  device interrupts only (IRQn ≥ 0).
- **No weak handlers, no magic names.** An unwired slot is NULL; if that
  IRQ ever fires the CPU branches to 0 with the Thumb bit clear →
  UsageFault/HardFault, caught in one place. "Enabled but unwired" is a
  loud, catchable bug, not a silent hang.
- **No required `main()`.** The app may define `Reset_Handler` directly;
  the table + handlers *are* the program.

**ROM vs RAM (one literal, three knobs):**

| | ROM | RAM |
|---|---|---|
| section / const | `.isr_vector` flash, `const` | `.ram_vectors` (≥512-B aligned), non-`const` |
| VTOR | flash base | boot copies flash→RAM, sets VTOR |
| runtime install | no | yes |

RAM still needs the flash boot stub (SP/Reset/faults) so the chip can
come up before VTOR moves — the one irreducible piece of ROM.

**NVIC is the non-OS core.** Runtime handler install is a first-class
NVIC operation, race-free:

```c
// lib/nvic.h (hand-written core)
static inline void nvic_install(enum IRQn_Type irq, isr_t fn) {
    int on = nvic_is_enabled(irq);
    nvic_disable(irq);
    __DMB();
    __ram_vectors[irq + 16] = fn;
    __DSB(); __ISB();
    if (on) nvic_enable(irq);
}
```

**Division of labour:**
- *Generator* emits: `IRQn_Type`, and `NVIC_VECTORS` (= 16 + maxIRQ + 1;
  118 on G4). Nothing else vector-related.
- *Hand-written lib* (`lib/nvic.h`, per core family): `isr_t`,
  `VECTOR()`, the boot stub + flash→RAM relocate, `nvic_install`,
  enable/disable/priority/pending — all built on the generated NVIC
  register struct. **Done** (M3 preview): `lib/nvic.h` exists and
  compiles to optimal code; the generated header carries a
  `NARRAY_DEVICE` sentinel macro (enums are invisible to the preprocessor)
  so the lib can require a device header first, plus `NVIC_VTOR_ALIGN`
  for the RAM table's alignment.

## Roadmap

Each milestone is independently useful.

**M0 — skeleton.** Lay out `n-array/{periph,gen,lib,boards,templates,
doc}`; move `stm32g4/*.periph` → `periph/stm32g4/`. (This doc is the
start of it.)

**M1 — `narray gen` reading `.periph` → headers. ← MOSTLY DONE.**
Go program (`gen/`): parses the `.periph` schema, resolves
`base/extend/exclude` inheritance, expands `<group>` arrays, fills
RESERVED gaps and unions, and emits the header. The header is written as
hand-written Go (`-header`), not a template — its layout logic (gaps,
unions, singleton accessor branching) reads clearer as code; templates
are reserved for boilerplate emitters where they earn their keep (and
devs.ld is simple enough to stay Go prints too). Named field-value enums
and `<group>` arrays are in. `NVIC_VECTORS` emitted; no `vectors.c`.
*Status:* header generates and compiles clean on host cc and
arm-none-eabi-gcc cortex-m4; 81 instance symbols, superset of the golden.
*Acceptance met* via compile + symbol/address coverage vs the golden
`stm32gen/.../stm32g4xx.h` (full structural diff still a nice-to-have).
*Remaining:* `devs.ld` emission; 16-bit register width (USB/RTC PMA).

**M2 — board/part layer.** Define `board.def`; generate device `.ld`
(memory sizes), filter instances/fields by prodcategory, emit `pinmux.h`.
*Acceptance:* a complete buildable register layer for one real part
(G431) with no hand-edits.

**M3 — driver library.** Consolidate gpio2/clock/boot/fault/tprintf/
fifo/nvic into `lib/`, reconciling divergent copies; clock-tree
data-driven from board.def. Per-family `arm_cmX.h` already exist in
`stm32gen/lib`. Hold CAN/USB/SPI back.

**M4 — project assembler.** `narray new <board.def>` → directory with
generated headers, selected drivers, Makefile, `.ld`, stub `main.c`.
*Acceptance:* an existing project rebuilds equivalently from a generated
skeleton.

**M5 — more families.** Extend `.periph` to L4/G0; validate the
generator generalizes. H7/dual-core last.

## Open questions (decide when reached)

- `board.def` format: XML (consistent with `.periph`), or something
  terser (TOML/Go struct)? Lean terse — it's hand-written per project.
- Clock tree: fully data-driven solver, or per-family hand-written
  `clock.c` parameterized by a few board.def numbers? Start hand-written.
- How to express a pinout conflict check (two functions on one pin) —
  generator-time error from `board.def` + `PINMUX.periph`.

## Reference points in the tree

- Output style + templates to borrow: `stm32gen/genstruct/{genstruct.go,
  header.h.tmpl,vector.c.tmpl,devs.ld.tmpl}`.
- A clean reference project (drivers + Makefile + boot): `stm32gen/
  stm32g473_quickcheck/`.
- Core headers per family: `stm32gen/lib/arm_cm{0plus,3,4,7}.h`.
- The data + schema: `n-array/stm32g4/` (`SCHEMA.md`, `*.periph`).
- The extraction skill (how `.periph` is produced from PDFs):
  `n-array/claude/stm32-periph-extraction.skill`.
</content>
</invoke>
