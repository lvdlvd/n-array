# [N]Array — design & roadmap

Status: generator + register headers, devs.ld, pinmux, memory linker
scripts, part/family selection, pin numbers all working; libs nvic/gpio/
clock/fifo/dma/serial/tprintf/console/fault in place, and a Nucleo-G474RE
`examples/hello` that builds the whole stack into a flashable image
(verified: correct vector-table manifest, .data LMA/VMA, CCRAM crashdump).
Compile+link clean on arm-none-eabi cortex-m4, gnu23. Next: flash it on
real hardware (still not hardware-validated).
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
- **C dialect: `gnu23`** (latest stable; `gnu17` also works). C23 makes
  64-bit enum constants standard ISO, which the pinmux uses; generated
  headers compile clean under `gnu23` with no `-Wno-pedantic` needed.
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
- **pinmux.h** validated AF constants from `PINMUX.periph` — see below.

## Pinmux & the board pinout (M2)

The board's defining artifact is its **pinout table** — the
`Pin | Port | Function | DIR | Config | Connected-to` markdown you
already author as hardware documentation (see
`compinator/doc/v2pinout.md`). Previously that table was hand-transcribed
into the role-enum + `pin_cfgs[]` at the top of `main.c`, choosing AF
numbers by hand — error-prone: b3dsm's `{TIM1CHx_PINS, GPIO_AF2_I2C3}`
labels AF2 "I2C3" though it's really TIM1 on those pins.

**Generated `pinmux.h` (`-pinmux`, done).** From the datasheet AF table
(`PINMUX.periph`) the generator emits one constant per *legal* (pin,
function) pair, AF baked in from the datasheet:

```c
enum GPIO_Mux {
  PA9_USART1_TX = (pinconf_t)PA9 | ((pinconf_t)(GPIO_AFOUT | (7 << 8)) << 32),
  PB3_FDCAN3_RX = ... // Cat3,4 only
};
```

A wrong pin/function pair simply doesn't exist → **compile error**
(`'PA9_USART2_TX' undeclared; did you mean 'PA2_USART2_TX'?`), not a
silent miswire. The board pinout becomes one compact, datasheet-checked
table:

```c
static const pinconf_t board[] = {
  PAAll|PIN_ANALOG, PBAll|PIN_ANALOG, PCAll|PIN_ANALOG, // unused -> off
  PC13           | PIN_OUTPUT,
  PA9_USART1_TX  | PIN_HIGH,
  PA10_USART1_RX | PIN_PULLUP,
  PB3_FDCAN3_RX,  PB4_FDCAN3_TX | PIN_HIGH,
};
```

**Encoding.** `pinconf_t` = `uint64_t`: low 32 = `GPIO_Pin` (port one-hot
+ index + 16-bit mask, so same-port pins still OR for bulk banks), high
32 = `GPIO_Conf` (mode/af/otype/pupd/speed). Config flags are
pre-shifted into the high word (`PIN_HIGH = (pinconf_t)GPIO_HIGH << 32`)
so they OR straight on. The whole header is **enums, not `#define`s**
(debugger-visible, per style.md): 64-bit enum constants are standard in
C23 and a pedantic-only warning before it. Storage stays `pinconf_t`, so
OR-combining constants needs no `-Wno-enum-conversion` — retiring that
old wart. The pin/port list is generated from the actual `GPIO`
instances (no `#if 0` for absent ports).

**Direction (decided): C is the source of truth, not markdown.** The
`board[]` table is the most-constrained representation (compiler +
datasheet validated), so it is canonical; the markdown is a generated
*view*. Three things are built on top:

- **`narray -pinfmt board.c`** — a "gofmt for pinouts". Each pin line
  carries a `//%` marker; narray resolves the constants (name-based,
  recognizing its own `pinmux.h` symbols), and rewrites an aligned,
  refreshed column block (pin, function, AF, attributes), preserving
  free-form text after a ` | ` separator. Idempotent; skips lines
  already inside a `//` comment or with no recognized pin.

      PA9_USART1_TX | PIN_HIGH,  //% PA9  USART1_TX  AF7  af,hi | console TX

- **`narray -pinout board.c`** — Markdown export of that same table, for
  docs (the generated view).
- **`lib/gpio.h` + `gpio.c`** (done) — the runtime: `gpioConfig(pinconf_t)`
  splits the 64-bit word (low = pins, high = conf) and programs MODER/
  OTYPER/OSPEEDR/PUPDR/AFRL/AFRH; `gpioConfigAll(board, n)`, `digitalHi/
  Lo/Toggle/In`, `gpioLock`. The port overlay `GPIO_ALL[8]` is an extern
  array (avoids `-Warray-bounds` from a single-object cast), bound to the
  first GPIO port by an alias narray emits into `devs.ld`. Compiles and
  links clean on arm-none-eabi cortex-m4; a board[] configures to
  absolute GPIO addresses.

**Header convention:** library files (`gpio.h/.c`) include the generated
register header as **`device.h`** — one canonical name per project, so a
project never includes both a family-specific name and `device.h` (which
would double-define under `#pragma once`). `nvic.h` still uses the
`NARRAY_DEVICE` sentinel guard (header-only, no `.c`); worth unifying on
`device.h` later.

**Physical pin numbers (done).** `PINMAP.periph` holds each GPIO pin's
number/coord per package (extracted from DS12712 Table 12, validated:
unique numbers per package; family-wide since the physical pinout is
pin-compatible). The full part number decodes to a package via the
ordering-info fields — `STM32G4{ff}{pincount}{flash}{pkgtype}{temp}`,
where **pin count (C/R/M/V/P/Q) and package type (H/I/T/U/Y) are
separate** (the part_decoder XML had conflated them). `narray -part
STM32G473RET6 -pinfmt board.c` then prepends the physical pin number
column (PA9 → 43 on LQFP64, 70 on LQFP100); `-pinout` adds a Pin column
and notes the package.

## Memory & linker scripts (M2)

Part-number-driven. The target is encoded in the `.ld` file name —
`STM32G473Exx_ROM` → family G473 (Cat3 SRAM map), flash suffix E
(512 KB), run model ROM. The Makefile selects part *and* run model by
linking `-T <name>.ld`.

**`narray -memory <name>`** (done) emits the device memory script from
`MEMORY.periph`: the `MEMORY` regions (FLASH by suffix, SRAM1/SRAM2/
CCMRAM), `_estack` at the top of CCMRAM, `INCLUDE devs.ld`, and the
`REGION_ALIAS` bindings + the right sections include.

**One boot, not boot_rom/boot_ram.** The ROM↔RAM difference is only
addresses, captured in linker symbols, so `lib/startup.h`'s
`narray_init_memory()` (the .data-copy / .bss-zero you flagged) is
identical for both — for RAM `_sidata == _sdata`, so the copy is a no-op.

**Two sections files, not one — an `ld` gotcha.** `REGION_ALIAS` gives
each alias its *own* position counter, so aliasing both `TEXT` and `DATA`
to one region makes them overlap. So:
- `lib/sections.ld` (ROM): `TEXT=FLASH`, `DATA=SRAM1`, `.data ... AT> LOAD`
  (LOAD=FLASH) — code in flash, data image copied to SRAM.
- `lib/sections_ram.ld` (RAM): a single alias `RUN`, everything packed
  in order (code, .data, .bss), `.data` in place (LMA==VMA).

Verified: both link clean on arm-none-eabi cortex-m4 with real .data/.bss;
ROM `.data` LMA in flash (copy), RAM LMA==VMA (no-op); `_estack` at CCMRAM
top.

**Part → family → header filtering (done).** Gating is by **device
family**, not category — Table 2 (RM0440 "Product specific features")
shows "Cat3" is not homogeneous (G473 has 5 ADC + TIM5 + dual-bank flash;
G491 has 3 ADC, no TIM5, single-bank). Elements carry `families="G473
G483 G474 G484"`; **absent = present in every G4 family**, so only the
bits that vary are tagged. `-part STM32G473RET6` resolves to the family
(G473) and drops any peripheral/instance/register/field whose `families=`
omits it. Verified against Table 2: HRTIM only G474/484; ADC5 G473/474/
484; TIM5/FDCAN3/FMC G473/474/484; AES only the x3/x4/441/4A1 devices;
etc. FLASH variants tagged by family (one struct per part, no collision);
no `-part` keeps the variant with the most registers (most complete).

Tagged so far: ADC3/4/5, DAC2/4, TIM5/20, FDCAN2/3, I2C4, HRTIM, FMC,
QUADSPI, AES, FLASH. **Still to tag** (intricate file structure):
SPI4, COMP5-7, OPAMP4-6, UART5. FLASH's dual/single-bank-by-family
nuance (G491 single-bank) is a known data limitation.

## Clock setup (`lib/clock.h` + `clock.c`, done)

Runtime-adaptive, not compile-time: the same firmware works across boards
regardless of crystal. `clock_measure_hse()` detects an HSE crystal and
*measures* it (RM0440 §7.2.16 — TIM16 input-captures HSE/32 against the
reset-default 16 MHz HSI), rounding to the nearest MHz; the value is
cached in `clock_hse_hz`. Preset targets `clock_init_16/64/144/168()`
bring SYSCLK up via the PLL off HSE (else HSI16) — choosing PLLM to land
the VCO input near 8 MHz and PLLN for the target — handling PWR
voltage/boost and flash wait-states in the RM-mandated order (modelled on
the proven `stm32gen/.../boot.c` sequence). Fancier clocks: hand-roll the
RCC tree.

Live rate queries read the *current* RCC config (valid after a preset or
a hand setup): `clock_sysclk/hclk/pclk1/pclk2_hz()`, the `*_timer_hz()`
×2-rule variants, and per-kind kernel-clock rates from the CCIPR muxes —
`clock_usart_hz(n)`, `clock_lpuart_hz()`, `clock_i2c_hz(n)`,
`clock_fdcan_hz()` — for UART BRR and CAN bit timing. Peripheral *enables*
stay the app's job (`RCC.APBxENR |= …` at the top of `main`).

Compiles and links clean on arm-none-eabi cortex-m4 (gnu23); **not yet
hardware-validated** (no device in the loop) — the measurement and PLL
sequence are compile-checked and modelled on working code.

Implementing this exposed and fixed a data bug: TIM16/17 `TISEL` was at
offset 0x68 (should be 0x5C = the OR1 register); corrected the offset,
added the `TI1SEL` source enums (incl. `HSE_32`=3, RM0440 Table 290) and
the missing `OR1`/`HSE32EN` register, so the measurement references
generated named constants.

**Earlier:** the last family-gating tags — SPI4 (added, was missing),
UART5; COMP/OPAMP left as over-provided arrays (documented; the access+
OPAMP set {1,2,3,6} isn't a range).

## Buffered serial: fifo / dma / serial (done)

Generalized from the reference projects' `fifo.h`/`dma.h`/`serial.h`:
- `lib/fifo.h` — lock-free circular byte buffer with block access
  (`fifo_head/tail` + `*_size` contiguous chunks), the DMA-shaped FIFO.
  Verbatim — already device-independent.
- `lib/dma.h` — channel helpers built directly on the generated
  `DMA_Type.CH[8]` sub-struct array (the old hand-rolled `struct
  DMA_Channel` + cast is gone — a concrete win of the richer generation)
  and `DMAMUX.C[16]`; `DMA_CHAN`/`DMA_REQ` enums, `dma_start_rx/tx`,
  `dma_stop`, `dma_isr`, `dma_remaining`.
- `lib/serial.h` — USART⇄DMA⇄FIFO: RX DMAs into the fifo head in
  ≤¼-buffer bursts with a receive-timeout flush; TX DMAs from the tail,
  chained by the TC IRQ. The app owns the vectors and calls
  `serial_*_handler()` from them (the [N]Array model). Init takes the
  runtime kernel rate: `usart_init_tx(&USART1, clock_usart_hz(1), baud)`.
  LPUART is unified via SERIAL_INITIALIZER_LP's `(USART_Type*)` cast — TX
  shares the Serial struct and handlers; only `lpuart_init_tx()` differs
  (256× `lpuart_brr`). LPUART has no `RTOR`, so its RX is DMA-only (no
  timeout flush). Whole stack compiles/links clean on cortex-m4 (gnu23).

This generalization exposed and fixed a generator gap: scalar-array
registers (DMAMUX `C[16]`, COMP `C[7]`, OPAMP `O[6]`) were emitting no
field enums. Now they emit field-mask + named-value enums (skip only the
per-element accessors), giving `DMAMUX_C_DMAREQ_ID`, `COMP_C_HYST`, etc.

## Console & crash handling: tprintf / console / fault (done)

- `lib/tprintf.{c,h}` — Marco Paland's MIT tiny-printf, imported verbatim
  (license intact). Self-contained, `_putchar`/`fctprintf` output hooks.
- `lib/console.{h,c}` — glue from tprintf to the serial FIFO:
  `serial_printf(s, ...)` (via `fctprintf`) to any port, and a global
  `console` + `_putchar` so plain `tprintf()`/`printf` work. One writer
  per console FIFO.
- `lib/fault.{c,h}` — fault/assert/stray-IRQ trapping with a **post-mortem
  crash record** in a no-init `.crashdump` section at the **low end of
  CCRAM** (survives reset; the stack grows down from `_estack`, with
  `_stack_limit` = record + a 256 B guard gap above it). The naked fault
  handlers pick MSP/PSP from EXC_RETURN, record the causal PC/LR + cause
  (CFSR/HFSR/fault addr), and **harvest a call-chain by scanning the stack
  for Thumb return addresses** (odd, in `[_stext,_etext]`, preceded by a
  BL/BLX) — no unwind tables in ROM. Then BKPT if a debugger is attached
  (DHCSR), else reset. Stray IRQs use the NULL-vector model: pc≈0 ⇒ record
  the IRQ from the stacked xPSR. `__assert_func` captures file/line/expr +
  chain the same way. On the next boot, `fault_report(putc)` prints the
  record (incl. the raw backtrace addresses) once the console is up.
- `tools/narray-trace` — POSIX wrapper over `arm-none-eabi-addr2line`:
  paste the `CRASH …`/`backtrace:` output (or pass addresses) to get
  `0xADDR  function at file:line` for each frame. Symbolization is offline
  against the `.elf`, so nothing extra ships in ROM.

`.crashdump` placement and `_stext`/`_stack_limit` were added to both
`sections.ld` and `sections_ram.ld`. Whole console+fault stack compiles
and links clean on cortex-m4 (gnu23). **Stack-overflow detection** is
SP-vs-`_stack_limit` only for now (flags `overflow`, skips the walk); an
MPU guard page would make it airtight — deferred.

## SPI: transaction-queue driver (`lib/spi.{h,c}`, master done)

Generalized from a flight-tested STM32L4 master driver (`bminator10/src/spi.c`)
into a reusable n-array building block. The core idea is **one ring of
fixed-size transaction records (`struct SPIXmit`) with three cursors** —
`head ≥ curr ≥ tail`:

```
elem[]:  [ done ][ done ][  IN FLIGHT  ][ pending ][ free ]
         ^tail                ^curr                 ^head
         `- deq results       `- DMA exchanging     `- enq here
```

`[tail,curr)` are completed transactions awaiting `spiq_deq_tail()`,
`elem[curr]` is the one DMA is exchanging now, `[curr,head)` are enqueued
and waiting. The single ring is thus *both* the input and output FIFO. Each
transaction is **full-duplex in place**: TX-DMA clocks `buf` out while RX-DMA
writes the received bytes back into the same `buf`. `spiq_enq_head()` kicks
the SPI only if idle; otherwise the RX-DMA completion ISR chains to the next.

API (kept deliberately parallel for a future I2C twin): `spiq_init`,
`spiq_head`/`spiq_enq_head` to enqueue, `spiq_tail`/`spiq_deq_tail` to collect,
`spi_rx_dma_handler` to call from the RX channel's IRQ, `spi_wait` (WFI), and
the synchronous `spiq_xmit` convenience. A `ss_func(spi, addr, on)` hook
replaces hardware NSS and, since it gets the `spi`, can retune speed/polarity
per slave.

What changed from the L4 original for n-array/G4:
- DMA via `lib/dma.h` (DMAMUX) — the hand-rolled channel overlay, the fixed
  `enum spiq_dma_t` SPI↔channel combos, and `DMA_CSELR` are all gone. The
  caller picks any two `DMA_CHAN`; the driver derives `DMA_REQ_SPIx_RX/TX`
  from the `SPI_Type*` and sets the mux.
- Registers from the generated header; `clock_div` is a generated
  `SPI_CR1_BR_Div*` value (pre-shifted, OR'd straight into CR1), framing set
  explicit 8-bit via `SPI_CR2_DS_Bits8 | SPI_CR2_FRXTH_Quarter`.
- **App owns IRQs** (n-array convention): `spiq_init` does *not* touch the
  NVIC. The app wires only the **RX** channel's vector slot to a handler
  calling `spi_rx_dma_handler` and `nvic_enable`s that one IRQ. The TX channel
  needs no NVIC line; its TCIF (set because `dma_start_tx` enables TCIE) is
  cleared inside the handler. Completing on RX-done is correct: RX finishes
  exactly when the last byte has been clocked in.
- Transaction buffer is **embedded** (`buf[SPI_XMIT_BUF]`, default 32) — DMA-
  safe, bounded, no lifetime worries (decided in review over a pointer model).
  `SPI_QUEUE_LEN` (default 16, power of two) and `SPI_XMIT_BUF` are override
  macros.

*Status:* compiles clean on cortex-m4 (gnu23, `-Wextra`/`-Wenum-conversion`),
788 B text. **Not hardware-validated** (no board) — like the rest of the
runtime pieces. No SPI example yet.

*Deferred (designed, not built):*
- **I2C** with a deliberately parallel API: same three-cursor ring, an
  `I2CXmit { addr, wlen, rlen, buf }` (write-then-read with repeated START),
  engine driving `CR2.SADD/NBYTES/RD_WRN/START/AUTOEND` + a small
  `i2c_irq_handler` for STOP/NACK. No `ss_func` (7-bit addr is in the record).
  Kept as separate parallel files, not a shared generic ring (rule of 3–5;
  only two users).
- **Slave mode (stm-to-stm)** reusing the same `SPIQ`, but NSS-edge driven:
  RX/TX DMA kept armed, the master's NSS-deassert (EXTI on the NSS pin) ends
  the frame and `dma_remaining()` gives the actual length. The earlier
  L4↔RPi5 slave attempt (in old compinator commits) was abandoned over
  master-side timing; stm↔stm is tractable but is exactly the part compiling
  can't validate, so it waits for hardware.

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
`stm32gen/lib`. SPI master done (`lib/spi.{h,c}`); I2C (parallel API) and
SPI slave designed, deferred to hardware. Hold CAN/USB back.

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
