// [N]Array smoke test — generic 64-pin STM32G474 breakout: blink the LED (PC13)
// and print over the ST-Link virtual COM port (USART1, PA9/PA10) at 115200 8N1.
// Exercises the whole stack: generated register header + linker scripts, memory
// init, the vector table manifest, the clock lib (HSE auto-detect else HSI16 ->
// 168 MHz), GPIO, DMA+IRQ buffered serial, tprintf/console, and the fault/crash
// machinery.
//
// Flash with: make flash   (openocd via ST-Link). Open the VCP at 115200.

#include "device.h" // generated for STM32G474
#include "pinmux.h"

#include "clock.h"
#include "console.h" // pulls serial.h + tprintf.h
#include "fault.h"
#include "gpio.h"
#include "nvic.h"
#include "startup.h"

// A typed role name: keeps -Wenum-conversion checking and folds to an immediate
// at -O (no storage). Works as a runtime arg; a name needed in a static
// initializer (board[] below) must instead be a #define (a constant expression).
static const enum GPIO_Pin LED = PC13; // breakout LED (active low)

extern const isr_t __vectors[]; // the vector table, defined at the foot of the file

static uint8_t tx_buf[1024]; // power-of-two TX ring
static struct Serial vcp = SERIAL_INITIALIZER(USART1, tx_buf);

// The board pinout (run `narray -part STM32G474RET6 -pinfmt main.c` to annotate).
static const pinconf_t board[] = {
	// Unused -> analog (low power). EXCLUDE PA13/PA14: they are SWDIO/SWCLK and
	// blanketing them to analog drops the debugger off the running chip (then
	// only a BOOT0 entry to the system loader can reflash it).
	(PAAll & ~(Pin_13 | Pin_14)) | PIN_ANALOG, PBAll | PIN_ANALOG, PCAll | PIN_ANALOG,
	PC13 | PIN_OUTPUT,                                          //% breakout LED
	PA9_USART1_TX | PIN_HIGH,                                   //% ST-Link VCP TX
	PA10_USART1_RX | PIN_PULLUP,                                //% ST-Link VCP RX
};

// Console putc for the boot-time crash report. Polled/blocking on purpose: a
// post-mortem dump must not rely on the async DMA+IRQ TX engine, which may not
// drain before the program continues (or re-faults). usart_putc spins on TXE.
static void cputc(char c) { usart_putc(&USART1, c); }

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
	RCC.AHB2ENR |= RCC_AHB2ENR_GPIOAEN | RCC_AHB2ENR_GPIOCEN; // PA9/PA10 USART1, PC13 LED
	RCC.APB2ENR |= RCC_APB2ENR_USART1EN;

	gpioConfigAll(board, sizeof board / sizeof board[0]);

	dma_set_mux(DMA1_CH1, DMA_REQ_USART1_TX);
	usart_init_tx(&USART1, clock_usart_hz(1), 115200);
	console = &vcp;
	nvic_enable(DMA1_CH1_IRQn);
	nvic_enable(USART1_IRQn);

	fault_report(cputc); // print a crash from the previous run, if any
	tprintf("\n[N]Array hello on STM32G474, sysclk = %u Hz, hse = %u Hz\n",
	        (unsigned)clock_sysclk_hz(), (unsigned)clock_hse_hz);

	// To smoke-test the crash path, uncomment: faults, captures, resets, and
	// the line above prints the decoded crash + NARRAY-BT trace next boot.
	 *(volatile uint32_t *)0 = 0;

	for (uint32_t tick = 0;; tick++) {
		digitalToggle(LED);
		tprintf("tick %u\n", (unsigned)tick);
		for (volatile uint32_t d = 0; d < 4000000; d++) {
		}
	}
}

// TX path interrupts (the app owns the vectors and calls the lib handlers).
static void dma1_ch1(void) { serial_dma_tx_handler(&vcp, DMA1_CH1); }
static void usart1(void) { serial_irq_tx_handler(&vcp, DMA1_CH1); }

extern void _estack(void); // top of stack (linker)

// The vector table IS the program: slot 0 = SP, 1 = Reset (positional), 3-6 the
// core fault handlers, device IRQs by VECTOR(IRQn). Unwired slots stay NULL and
// fault loudly if ever taken.
__attribute__((section(".isr_vector"))) const isr_t __vectors[NVIC_VECTORS] = {
	(isr_t)&_estack, // [0] SP    — positional
	Reset_Handler,   // [1] Reset — positional, no +16
	[VECTOR(HardFault_IRQn)] = HardFault_Handler,   // core exceptions sit at
	[VECTOR(MemManage_IRQn)] = MemManage_Handler,   // slots 3-6 (IRQn+16);
	[VECTOR(BusFault_IRQn)] = BusFault_Handler,     // VECTOR names them like
	[VECTOR(UsageFault_IRQn)] = UsageFault_Handler, // any device IRQ
	[VECTOR(DMA1_CH1_IRQn)] = dma1_ch1,
	[VECTOR(USART1_IRQn)] = usart1,
};
