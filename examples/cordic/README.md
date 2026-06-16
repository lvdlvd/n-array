# cordic — [N]Array CORDIC example (Nucleo-G474RE)

Drives the STM32G4 **CORDIC coprocessor** through a deterministic, seed-fixed
vector sweep covering every `FUNC`/`SCALE`/argument operating point the
`cordic.h` math frontend uses, and prints one hex record per operation over the
ST-Link virtual COM port (USART2 on PA2/PA3, 115200 8N1):

```
CSR ARG1 ARG2 RES1 RES2
```

The sweep itself ([device_dump.c](device_dump.c)) is imported verbatim — but for
its includes — from the standalone `cordic-math` project's test suite, so the
device output can be diffed **byte-for-byte** against the host emulation. A clean
diff proves the CORDIC silicon is bit-exact with the software model.

This example exercises the `lib/cordic*` integration (generated `CORDIC`
singleton, zero-overhead reads), the clock lib, GPIO, and a lossless **polled**
UART writer (`_putchar`) — not the DMA console, because every byte in a
diffed dump must survive a full FIFO.

## Build & flash

```sh
make            # generates device.h/pinmux.h/*.ld via narray, builds cordic.bin
make flash      # st-flash --reset write cordic.bin 0x08000000
```

Built with `-fno-builtin -ffp-contract=off` (no fast-math) so the compiler emits
real calls and the low mantissa bits match the host model.

## Verify against the host emulation

Generate the reference vectors from the `cordic-math` tree (host build, emulated
backend), capture the device output, and diff:

```sh
# 1. host reference (in the cordic-math project)
cd ../../../cordic-math && make vectors      # -> build/vectors_emul.txt (3531 records)

# 2. capture the board's output between the markers, then diff
#    (the dump is bracketed by NARRAY-CORDIC-DUMP … NARRAY-CORDIC-END)
sed -n '/NARRAY-CORDIC-DUMP/,/NARRAY-CORDIC-END/p' device_capture.txt \
    | grep -E '^[0-9A-F]{8} ' \
    | diff - ../../../cordic-math/build/vectors_emul.txt
```

No output from `diff` ⇒ silicon and emulation agree on every exercised point.
Any mismatch localizes to a specific `FUNC`/`SCALE`/argument and is resolved via
the calibration knobs in `cordic-math`'s `math_emul.c` (see that project's
README, "Calibration").

## Crash testing

The four core fault handlers are wired to the [N]Array fault machinery, and
`fault_report()` runs at boot. To exercise it, add a faulting line in
`Reset_Handler` (e.g. `*(volatile uint32_t *)0 = 0;`); the next boot prints the
decoded crash + a `NARRAY-BT … END` line, symbolizable offline:

```sh
pbpaste | ../../tools/narray-trace cordic.elf
```
