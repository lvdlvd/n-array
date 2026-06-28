# hello — [N]Array smoke test (generic STM32G474 breakout)

Blinks the LED (PC13, active low) and prints over the ST-Link virtual COM port
(USART1 on PA9/PA10, 115200 8N1). It exercises the whole stack in one image: the
narray-generated register header and linker scripts, memory init, the
vector-table manifest, the clock lib (HSE auto-detect else HSI16 → 168 MHz),
GPIO, DMA+IRQ buffered serial, `tprintf`/console, and the fault/crash machinery.

## Build & flash

```sh
make            # generates device.h/pinmux.h/*.ld via narray, builds hello.bin
make flash      # openocd via ST-Link (stlink.cfg + stm32g4x.cfg)
```

Open the VCP at 115200; you should see `[N]Array hello … sysclk = … Hz` then
`tick N` once per blink. Requires the arm-none-eabi GCC toolchain and Go (for
narray). `make pinfmt` refreshes the `//%` pinout annotations in `main.c` from
the datasheet.

## Other parts / boards

Change `PART`/`PARTFULL`/`MEMTARGET` in the Makefile (e.g. `STM32G431`,
`STM32G431Bxx_ROM`) and the pin/USART/clock choices in `main.c`. For a RAM-run
image, use the `_RAM` memory target.

## Crash testing

Uncomment the `*(volatile uint32_t *)0 = 0;` line in `main.c` to fault: the
handler captures a post-mortem to CCRAM and resets; on the next boot
`fault_report()` prints the decoded crash and a `NARRAY-BT … END` address line.
Symbolize it offline:

```sh
pbpaste | ../../tools/narray-trace hello.elf      # paste the console output
```
