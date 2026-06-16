// [N]Array smoke test — Nucleo-G474RE: blink LD2 (PA5) and print over the
// ST-Link virtual COM port (USART2, PA2/PA3) at 115200 8N1. Exercises the whole
// stack: generated register header + linker scripts, memory init, the vector
// table manifest, the clock lib (HSE auto-detect else HSI16 -> 168 MHz), GPIO,
// DMA+IRQ buffered serial, tprintf/console, and the fault/crash machinery.
//
// Flash with: make flash   (st-flash / openocd). Open the VCP at 115200.

#include "device.h" // generated for STM32G474
#include "pinmux.h"

#include "clock.h"
#include "console.h" // pulls serial.h + tprintf.h
#include "fault.h"
#include "gpio.h"
#include "nvic.h"
#include "startup.h"

#define LED PA5 // Nucleo LD2

extern const isr_t __vectors[]; // the vector table, defined at the foot of the file

static uint8_t tx_buf[1024]; // power-of-two TX ring
static struct Serial vcp = SERIAL_INITIALIZER(USART2, tx_buf);

// The board pinout (run `narray -part STM32G474RET6 -pinfmt main.c` to annotate).
static const pinconf_t board[] = {
	PAAll | PIN_ANALOG, PBAll | PIN_ANALOG, PCAll | PIN_ANALOG, // unused -> analog (low power)
	PA5 | PIN_OUTPUT,                                           //% LD2 LED
	PA2_USART2_TX | PIN_HIGH,                                   //% ST-Link VCP TX
	PA3_USART2_RX | PIN_PULLUP,                                 //% ST-Link VCP RX
};

// Console putc for the boot-time crash report (drains via the DMA TX engine).
static void cputc(char c) {
	serial_putc(c, &vcp);
	serial_dma_tx_start(&vcp);
}

void Reset_Handler(void) __attribute__((noreturn));
void Reset_Handler(void) {
	narray_init_memory();
	SCB.VTOR = (uint32_t)(uintptr_t)__vectors;
	*(volatile uint32_t *)0xE000ED88 |= 0xfu << 20; // FPU: CP10/CP11 full access
	SCB.SHCSR |= SCB_SHCSR_USGFAULTENA;             // route usage faults to our handler
	SCB.CCR |= SCB_CCR_DIV_0_TRP;                   // div-by-zero -> UsageFault

	clock_init_168(); // HSE if present (auto-measured), else HSI16

	// peripheral clocks are the application's job
	RCC.AHB1ENR |= RCC_AHB1ENR_DMA1EN | RCC_AHB1ENR_DMAMUX1EN;
	RCC.AHB2ENR |= RCC_AHB2ENR_GPIOAEN;
	RCC.APB1ENR1 |= RCC_APB1ENR1_USART2EN;

	gpioConfigAll(board, sizeof board / sizeof board[0]);

	dma_set_mux(DMA1_CH1, DMA_REQ_USART2_TX);
	usart_init_tx(&USART2, clock_usart_hz(2), 115200);
	console = &vcp;
	nvic_enable(DMA1_CH1_IRQn);
	nvic_enable(USART2_IRQn);

	fault_report(cputc); // print a crash from the previous run, if any
	tprintf("\n[N]Array hello on STM32G474, sysclk = %u Hz, hse = %u Hz\n",
	        (unsigned)clock_sysclk_hz(), (unsigned)clock_hse_hz);

	// To smoke-test the crash path, uncomment: faults, captures, resets, and
	// the line above prints the decoded crash + NARRAY-BT trace next boot.
	// *(volatile uint32_t *)0 = 0;

	for (uint32_t tick = 0;; tick++) {
		digitalToggle(LED);
		tprintf("tick %u\n", (unsigned)tick);
		for (volatile uint32_t d = 0; d < 4000000; d++) {
		}
	}
}

// TX path interrupts (the app owns the vectors and calls the lib handlers).
static void dma1_ch1(void) { serial_dma_tx_handler(&vcp, DMA1_CH1); }
static void usart2(void) { serial_irq_tx_handler(&vcp, DMA1_CH1); }

extern void _estack(void); // top of stack (linker)

// The vector table IS the program: slot 0 = SP, 1 = Reset (positional), 3-6 the
// core fault handlers, device IRQs by VECTOR(IRQn). Unwired slots stay NULL and
// fault loudly if ever taken.
__attribute__((section(".isr_vector"))) const isr_t __vectors[NVIC_VECTORS] = {
	(isr_t)&_estack,
	Reset_Handler,
	[3] = HardFault_Handler,
	[4] = MemManage_Handler,
	[5] = BusFault_Handler,
	[6] = UsageFault_Handler,
	[VECTOR(DMA1_CH1_IRQn)] = dma1_ch1,
	[VECTOR(USART2_IRQn)] = usart2,
};
