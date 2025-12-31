# STM32G4 Peripheral Register Definitions

Accurate peripheral register definitions extracted from ST's RM0440 Reference Manual (Rev 7) for the STM32G4 microcontroller family.

## Purpose

The official CMSIS-SVD files from ST contain numerous errors, omissions, and inconsistencies. This project provides hand-extracted, validated register definitions in a clean XML format suitable for code generation, documentation, or hardware abstraction layer development.

## Contents

- **43 peripheral definition files** (`.periph` extension)
  - 35 STM32G4 device peripherals (from RM0440)
  - 4 Cortex-M4 core peripherals (from PM0214)
  - 2 system info files (device signature, interrupts)
  - 1 pin multiplexing file (from datasheets)
  - 1 errata file (from ES0430/ES0431/ES0523)
- **SCHEMA.md** - XML schema documentation
- This README

## Peripheral Coverage

### Communication
| Peripheral | Description | Instances |
|------------|-------------|-----------|
| USART | UART with LIN, IrDA, Smartcard, RS-485 | USART1-3, UART4-5, LPUART1 |
| SPI | SPI with I2S support | SPI1-4 |
| I2C | I2C with SMBus/PMBus | I2C1-4 |
| FDCAN | CAN FD with message RAM | FDCAN1-3 |
| USB | Full-speed USB with packet memory | USB1 |
| SAI | Serial Audio Interface | SAI1 |
| UCPD | USB Type-C Power Delivery | UCPD1 |

### Analog
| Peripheral | Description | Instances |
|------------|-------------|-----------|
| ADC | 12-bit SAR ADC with injected channels | ADC1-5 + common |
| DAC | 12-bit DAC with waveform generation | DAC1-4 |
| COMP | Comparators with hysteresis | COMP1-7 |
| OPAMP | Operational amplifiers with PGA | OPAMP1-6 |
| VREFBUF | Voltage reference buffer | 1 |

### Timers
| Peripheral | Description | Instances |
|------------|-------------|-----------|
| TIM | General/Advanced timers | TIM1-8, TIM15-17, TIM20 |
| HRTIM | High-resolution timer (184ps) | 1 (6 timer units) |
| LPTIM | Low-power timer | LPTIM1 |

### DMA & Memory
| Peripheral | Description | Instances |
|------------|-------------|-----------|
| DMA | DMA controller | DMA1-2 (8 channels each) |
| DMAMUX | DMA request multiplexer | 1 (16 channels) |
| FMC | Flexible memory controller | 1 |
| QUADSPI | Quad-SPI Flash interface | 1 |
| FLASH | Embedded Flash with ECC | 1 |

### System & Security
| Peripheral | Description | Instances |
|------------|-------------|-----------|
| RCC | Reset and clock control | 1 |
| PWR | Power control | 1 |
| SYSCFG | System configuration | 1 |
| EXTI | Extended interrupts | 1 |
| GPIO | General-purpose I/O | GPIOA-G |
| RNG | Random number generator | 1 |
| CRC | CRC calculation unit | 1 |
| AES | AES hardware accelerator | 1 |
| RTC | Real-time clock | 1 |
| TAMP | Tamper detection + backup registers | 1 |
| IWDG | Independent watchdog | 1 |
| WWDG | Window watchdog | 1 |
| CORDIC | Trigonometric accelerator | 1 |
| FMAC | Filter math accelerator | 1 |
| DBGMCU | Debug MCU support | 1 |

## XML Schema Features

### Basic Structure
```xml
<peripheral name="USART" description="...">
  <instance id="1" base="0x40013800" />
  <instance id="2" base="0x40004400" />
  
  <register name="CR1" offset="0x00" reset="0x00000000" description="...">
    <field name="UE" mask="0x00000001" access="rw" description="USART enable" />
    <field name="M0" mask="0x00001000" access="rw" description="Word length bit 0" />
  </register>
</peripheral>
```

### Inheritance Model
```xml
<!-- Abstract base peripheral (template only) -->
<peripheral name="TIM_BASIC" abstract="true" description="Basic timer">
  <register name="CR1" ... />
</peripheral>

<!-- Derived peripheral inheriting from base -->
<peripheral name="TIM_GP16" base="TIM_BASIC" description="16-bit GP timer">
  <!-- Adds new registers, inherits CR1 -->
  <register name="CCMR1" ... />
</peripheral>

<!-- Extend inherited register -->
<peripheral name="TIM_ADV" base="TIM_GP16">
  <register name="CR1" extend="true">
    <!-- Add fields not in base -->
    <field name="UIFREMAP" mask="0x00000800" access="rw" />
  </register>
</peripheral>
```

### Register Groups (Arrays)
For repeated register blocks like DMA channels:
```xml
<group name="CH" offset="0x08" stride="0x14" range_min="1" range_max="8" 
       description="DMA channel">
  <register name="CR" offset="0x00" ... />
  <register name="NDTR" offset="0x04" ... />
  <register name="PAR" offset="0x08" ... />
  <register name="MAR" offset="0x0C" ... />
</group>
```
Address formula: `base + group_offset + (index - range_min) * stride + register_offset`

### Device Variant Support
```xml
<peripheral name="FLASH" prodcategory="3">
  <!-- Only present on Cat3 devices -->
</peripheral>

<field name="DBANK" mask="0x00400000" prodcategory="4">
  <!-- Field only on Cat4 devices (dual-bank flash) -->
</field>
```

### Access Types
| Type | Description |
|------|-------------|
| `rw` | Read-write |
| `ro` | Read-only |
| `wo` | Write-only |
| `rc_w1` | Read, clear by writing 1 |
| `rc_w0` | Read, clear by writing 0 |
| `rs` | Read, set by software (cleared by hardware) |
| `t` | Toggle on write 1 |

### RAM Elements
For peripherals with dedicated memory (FDCAN message RAM, USB packet memory):
```xml
<ram_elements base="0x..." size="2560" description="Message RAM" access_width="32">
  <element name="FILTER_STD" size="128" description="Standard ID filters">
    <word index="0">
      <field name="SFID2" mask="0x0000FFFF" description="..." />
      <field name="SFID1" mask="0xFFFF0000" description="..." />
    </word>
  </element>
</ram_elements>
```

## STM32G4 Product Categories

The STM32G4 family has different feature sets by product category:

| Category | Flash | ADCs | Timers | Notes |
|----------|-------|------|--------|-------|
| Cat2 | Up to 128KB | ADC1-2 | Basic set | Entry-level |
| Cat3 | Up to 256KB | ADC1-2 | + HRTIM | Motor control |
| Cat4 | Up to 512KB | ADC1-5 | Full set | Dual-bank flash |

## Usage Examples

### Code Generation
These files can drive code generators for:
- C/C++ register headers with bitfield structs
- Rust PAC (Peripheral Access Crate) generation
- Hardware abstraction layers
- Register documentation

### Parsing Example (Python)
```python
import xml.etree.ElementTree as ET

tree = ET.parse('GPIO.periph')
root = tree.getroot()

for peripheral in root.findall('peripheral'):
    print(f"Peripheral: {peripheral.get('name')}")
    for reg in peripheral.findall('register'):
        offset = reg.get('offset')
        print(f"  {reg.get('name')} @ {offset}")
```

## Source

All data extracted from:
- **Document:** RM0440 Reference Manual
- **Revision:** Rev 7 (latest as of extraction)
- **Manufacturer:** STMicroelectronics

## Core Peripherals (from PM0214)

The following ARM Cortex-M4 core peripherals are included:

| Peripheral | Base | Description |
|------------|------|-------------|
| NVIC | 0xE000E100 | Nested Vectored Interrupt Controller |
| SCB | 0xE000ED00 | System Control Block |
| STK | 0xE000E010 | SysTick Timer |
| MPU | 0xE000ED90 | Memory Protection Unit |

## System Information

| File | Description |
|------|-------------|
| DEVSIG.periph | Device signature (UID, flash size, package) and calibration data (TSCAL1/2, VREFINT) |
| INTERRUPTS.periph | Interrupt vector table (IRQ numbers 0-101) |

## Not Included

- **IRTIM** - No dedicated registers (uses TIM16/TIM17 outputs)

## License

This register data is derived from publicly available ST documentation. Use according to ST's documentation terms.

## Version

- Extraction date: December 2024
- Schema version: 1.0

## Errata Reference

The `ERRATA.periph` file contains known silicon errata extracted from:
- ES0430 Rev 9 (G471/G473/G474/G483/G484)
- ES0431 Rev 9 (G431/G441)  
- ES0523 Rev 5 (G491/G4A1)

Each erratum includes:
- Affected silicon revisions (Z, Y, X or A, Z)
- Workaround status (A=available, P=partial, N=none)
- Affected registers
- Description and recommended workaround

Example usage in code generation:
```c
// Check for ADC erratum 2.7.9 - Wrong result if conversion late after calibration
// Workaround: Start conversion within 4 ADC clocks of ADEN
ADC1->CR |= ADC_CR_ADEN;
while (!(ADC1->ISR & ADC_ISR_ADRDY)); // Wait for ready
ADC1->CR |= ADC_CR_ADSTART;           // Start immediately
```
