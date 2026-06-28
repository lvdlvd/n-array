// [N]Array CORDIC example — generic 64-pin STM32G474 breakout: drive the CORDIC
// coprocessor through the deterministic backend-level vector sweep (device_dump.c,
// imported from the cordic-math test suite) and print one hex record per operation
// over the ST-Link virtual COM port (USART1, PA9/PA10, 115200 8N1):
//
//     CSR ARG1 ARG2 RES1 RES2
//
// Capture the output and diff it against vectors_emul.txt from cordic-math's host
// build: a clean diff proves the CORDIC silicon is bit-exact with the software
// model over every FUNC/SCALE operating point the math frontend uses. See README.
//
// Output is via a polled UART writer (not the DMA console): every byte matters in
// a dump that gets diffed, so we never drop on a full FIFO.

#include "device.h" // generated for STM32G474
#include "pinmux.h"

#include "clock.h"
#include "fault.h"
#include "gpio.h"
#include "nvic.h"    // isr_t, VECTOR()
#include "serial.h"  // usart_init_tx + the ISR/BRR helpers
#include "startup.h"
#include "tprintf.h" // _putchar / tprintf

// The deterministic sweep, imported verbatim-but-for-includes from
// cordic-math/test/device_dump.c (compiled with -DCM_DUMP_NO_MAIN).
int cm_dump_main(void);
void cm_dump_psweep(void); // diagnostic precision sweep (see device_dump.c)

extern const isr_t __vectors[];

// The board pinout (run `make pinfmt` to refresh these //% annotations).
static const pinconf_t board[] = {
	PAAll | PIN_ANALOG, PBAll | PIN_ANALOG, PCAll | PIN_ANALOG, // unused -> analog
	PA9_USART1_TX | PIN_HIGH,                                   //% ST-Link VCP TX
	PA10_USART1_RX | PIN_PULLUP,                                //% ST-Link VCP RX
};

// Lossless polled transmit: tprintf calls this for every character. usart_putc
// spins until the TX data register is empty, then hands over one byte. No FIFO,
// no DMA, no dropping — exactly what a diff-against-reference dump (and the crash
// report below) needs.
void _putchar(char c) { usart_putc(&USART1, c); }

void Reset_Handler(void) __attribute__((noreturn));
void Reset_Handler(void) {
	narray_init_memory();
	SCB.VTOR = (uint32_t)(uintptr_t)__vectors;
	*(volatile uint32_t *)0xE000ED88 |= 0xfu << 20; // FPU: CP10/CP11 full access
	SCB.SHCSR |= SCB_SHCSR_USGFAULTENA;

	clock_init_168(); // HSE if present (auto-measured), else HSI16

	// Peripheral clocks: GPIOA, USART1, and the CORDIC coprocessor.
	RCC.AHB1ENR |= RCC_AHB1ENR_CORDICEN;
	RCC.AHB2ENR |= RCC_AHB2ENR_GPIOAEN;
	RCC.APB2ENR |= RCC_APB2ENR_USART1EN;

	gpioConfigAll(board, sizeof board / sizeof board[0]);
	usart_init_tx(&USART1, clock_usart_hz(1), 115200);

	fault_report(_putchar); // report a crash from the previous run, if any

	// A clean start marker: the offline diff/capture ignores everything before
	// it, so pasted console noise (boot banners, prompts) does not matter.
	tprintf("\nNARRAY-CORDIC-DUMP sysclk=%u\n", (unsigned)clock_sysclk_hz());
	cm_dump_main(); // the deterministic FUNC/SCALE/arg sweep
	tprintf("NARRAY-CORDIC-END\n");

	tprintf("\nNARRAY-PSWEEP-DUMP\n"); // diagnostic: sin/cos at precision 1..6
	cm_dump_psweep();
	tprintf("NARRAY-PSWEEP-END\n");

	for (;;) {
	}
}

// Minimal manifest: SP, Reset, and the core fault handlers. No device IRQs — the
// dump is fully synchronous (polled UART, zero-overhead CORDIC reads).
extern void _estack(void);
__attribute__((section(".isr_vector"))) const isr_t __vectors[NVIC_VECTORS] = {
	(isr_t)&_estack,
	Reset_Handler,
	[VECTOR(HardFault_IRQn)] = HardFault_Handler,
	[VECTOR(MemManage_IRQn)] = MemManage_Handler,
	[VECTOR(BusFault_IRQn)] = BusFault_Handler,
	[VECTOR(UsageFault_IRQn)] = UsageFault_Handler,
};
