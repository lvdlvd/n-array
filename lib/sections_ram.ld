/*
 * RAM-run section layout for [N]Array. The per-part memory script binds the
 * single alias RUN to the run RAM before INCLUDE-ing this file; everything
 * packs into RUN sequentially (code at the bottom, then .data, then .bss — the
 * "hole" the code occupies is implicit). .data is in place, so LMA == VMA and
 * narray_init_memory()'s copy is a no-op.
 *
 * One alias (one position counter): two aliases to the same region would each
 * start at its base and overlap, so the ROM layout's TEXT/DATA split is not
 * usable here — hence the separate file.
 */

ENTRY(Reset_Handler)
EXTERN(__vectors)

SECTIONS
{
  .isr_vector : {
    . = ALIGN(4);
    KEEP(*(.isr_vector))
    . = ALIGN(4);
  } > RUN

  .text : {
    . = ALIGN(4);
    _stext = .;                  /* code span, for the fault backtrace scanner */
    *(.text) *(.text.*)
    *(.rodata) *(.rodata.*)
    . = ALIGN(4);
    _etext = .;
  } > RUN

  .data : {
    . = ALIGN(4);
    _sdata = .;
    *(.data) *(SORT_BY_ALIGNMENT(.data.*))
    . = ALIGN(4);
    _edata = .;
  } > RUN
  _sidata = _sdata; /* in place: no startup copy */

  .bss : {
    . = ALIGN(4);
    _sbss = .;
    *(.bss) *(SORT_BY_ALIGNMENT(.bss.*)) *(COMMON)
    . = ALIGN(4);
    _ebss = .;
  } > RUN

  /* lib/fault.c crash record at the low end of CCRAM (NOLOAD, survives reset);
     stack grows down from _estack (top of CCRAM). */
  .crashdump (NOLOAD) : {
    . = ALIGN(8);
    KEEP(*(.crashdump))
    . = ALIGN(8);
  } > CCMRAM
  _stack_limit = ORIGIN(CCMRAM) + SIZEOF(.crashdump) + 256; /* + guard gap */

  /DISCARD/ : {
    *(.ARM.exidx) *(.ARM.exidx.*)
  }
}
