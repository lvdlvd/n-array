#pragma once

// Startup memory init for [N]Array. Call narray_init_memory() first thing in
// Reset_Handler: it copies the .data initialiser image to RAM and zeroes .bss
// using the symbols from lib/sections.ld. Run-model-independent — for a RAM
// build _sidata == _sdata so the copy loop is a no-op.
//
// The application owns Reset_Handler (the vector table is the program); a
// minimal one is:
//
//   void Reset_Handler(void) {
//       narray_init_memory();
//       SCB.VTOR = (uint32_t)(uintptr_t)__vectors;  // point at our table
//       ... clock, fpu ...
//       main();
//   }

#include <stdint.h>

extern uint32_t _sidata, _sdata, _edata, _sbss, _ebss; // provided by sections.ld

static inline void narray_init_memory(void) {
	for (uint32_t *src = &_sidata, *dst = &_sdata; dst < &_edata;) {
		*dst++ = *src++;
	}
	for (uint32_t *dst = &_sbss; dst < &_ebss; dst++) {
		*dst = 0;
	}
}
