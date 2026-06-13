# STM32G4 Peripheral XML Schema Reference

## Document Structure

```xml
<?xml version="1.0" encoding="UTF-8"?>
<device name="STM32G4" reference_manual="RM0440" revision="7">
  <peripheral>...</peripheral>
</device>
```

## Peripheral Element

```xml
<peripheral 
    name="NAME"                    <!-- Required: Peripheral name -->
    description="..."              <!-- Required: Human-readable description -->
    abstract="true"                <!-- Optional: Template only, no instances -->
    base="OTHER_PERIPHERAL"        <!-- Optional: Inherit from another peripheral -->
    prodcategory="2|3|4"           <!-- Optional: Device category restriction -->
>
```

### Attributes

| Attribute      | Required | Description                                                  |
| -------------- | -------- | ------------------------------------------------------------ |
| `name`         | Yes      | Unique peripheral identifier                                 |
| `description`  | Yes      | Human-readable description                                   |
| `abstract`     | No       | If "true", peripheral is a template (no instances generated) |
| `base`         | No       | Name of peripheral to inherit from                           |
| `prodcategory` | No       | STM32G4 product category (2, 3, or 4)                        |

## Instance Element

```xml
<instance 
    id="N"                         <!-- Required: Instance number -->
    base="0xNNNNNNNN"              <!-- Required: Base address -->
    prodcategory="2|3|4"           <!-- Optional: Category restriction -->
/>
```

### Examples
```xml
<instance id="1" base="0x40013800" />                    <!-- USART1 -->
<instance id="2" base="0x40004400" />                    <!-- USART2 -->
<instance id="5" base="0x50000600" prodcategory="4" />   <!-- ADC5, Cat4 only -->
```

## Register Element

```xml
<register 
    name="NAME"                    <!-- Required: Register name -->
    offset="0xNN"                  <!-- Required: Offset from peripheral base -->
    reset="0xNNNNNNNN"             <!-- Required: Reset value -->
    description="..."              <!-- Required: Description -->
    access="ro|wo|rw"              <!-- Optional: Default access for all fields -->
    extend="true"                  <!-- Optional: Extend inherited register -->
    prodcategory="2|3|4"           <!-- Optional: Category restriction -->
>
```

### Extending Inherited Registers
When a peripheral inherits from a base (`base="..."`), use `extend="true"` to add fields:

```xml
<!-- In base peripheral -->
<register name="CR1" offset="0x00" reset="0x00000000">
  <field name="EN" mask="0x00000001" access="rw" />
</register>

<!-- In derived peripheral -->
<register name="CR1" extend="true">
  <!-- Adds to existing fields, doesn't replace -->
  <field name="NEWBIT" mask="0x00000100" access="rw" />
</register>
```

## Field Element

```xml
<field 
    name="NAME"                    <!-- Required: Field name -->
    mask="0xNNNNNNNN"              <!-- Required: Bit mask -->
    access="ACCESS_TYPE"           <!-- Required: Access type -->
    description="..."              <!-- Required: Description -->
    prodcategory="2|3|4"           <!-- Optional: Category restriction -->
/>
```

### Access Types

| Type    | Description            | Read Behavior       | Write Behavior            |
| ------- | ---------------------- | ------------------- | ------------------------- |
| `rw`    | Read-write             | Returns value       | Sets value                |
| `ro`    | Read-only              | Returns value       | Ignored                   |
| `wo`    | Write-only             | Returns 0/undefined | Sets value                |
| `rc_w1` | Read, clear on write 1 | Returns value       | Writing 1 clears bit      |
| `rc_w0` | Read, clear on write 0 | Returns value       | Writing 0 clears bit      |
| `rs`    | Read, set by software  | Returns value       | Writing 1 sets, HW clears |
| `t`     | Toggle                 | Returns value       | Writing 1 toggles bit     |

### Field Examples
```xml
<!-- Standard read-write field -->
<field name="UE" mask="0x00000001" access="rw" description="USART enable" />

<!-- Interrupt flag (read, clear by writing 1) -->
<field name="TC" mask="0x00000040" access="rc_w1" description="Transmission complete" />

<!-- Status flag (read-only) -->
<field name="BUSY" mask="0x00010000" access="ro" description="Busy flag" />

<!-- Software trigger (set by SW, cleared by HW) -->
<field name="SWSTART" mask="0x00000004" access="rs" description="Start conversion" />
```

## Enum Element

```xml
<field name="FIELD_NAME" mask="0x..." access="rw" description="...">
  <enum value="0xNNNNNNNN" name="ENUM_NAME" description="..." />
  <enum value="0xNNNNNNNN" name="ENUM_NAME" />
</field>
```

### Example
```xml
<field name="PRESC" mask="0x00000E00" access="rw" description="Clock prescaler">
  <enum value="0x00000000" name="Div1" description="No division" />
  <enum value="0x00000200" name="Div2" description="Divide by 2" />
  <enum value="0x00000400" name="Div4" description="Divide by 4" />
  <enum value="0x00000600" name="Div8" description="Divide by 8" />
</field>
```

**Note:** Enum `value` is the full masked value, not just the field bits.

## Group Element (Register Arrays)

For repeated register blocks with regular spacing:

```xml
<group 
    name="PREFIX"                  <!-- Required: Name prefix for instances -->
    offset="0xNN"                  <!-- Required: Offset of first group -->
    stride="0xNN"                  <!-- Required: Bytes between groups -->
    range_min="N"                  <!-- Required: First index -->
    range_max="N"                  <!-- Required: Last index -->
    description="..."              <!-- Required: Description -->
>
  <register name="REG1" offset="0x00" ... />
  <register name="REG2" offset="0x04" ... />
</group>
```

### Address Calculation
```
register_address = peripheral_base 
                 + group_offset 
                 + (index - range_min) * stride 
                 + register_offset
```

### Example: DMA Channels
```xml
<group name="CH" offset="0x08" stride="0x14" range_min="1" range_max="8" 
       description="DMA channel">
  <register name="CR" offset="0x00" reset="0x00000000" description="Control" />
  <register name="NDTR" offset="0x04" reset="0x00000000" description="Data count" />
  <register name="PAR" offset="0x08" reset="0x00000000" description="Peripheral addr" />
  <register name="MAR" offset="0x0C" reset="0x00000000" description="Memory addr" />
</group>
```

For DMA1 (base 0x40020000):
- CH[1].CR = 0x40020000 + 0x08 + (1-1)*0x14 + 0x00 = 0x40020008
- CH[2].CR = 0x40020000 + 0x08 + (2-1)*0x14 + 0x00 = 0x4002001C
- CH[1].NDTR = 0x40020000 + 0x08 + (1-1)*0x14 + 0x04 = 0x4002000C

## Exclude Element

Remove inherited fields in derived peripherals:

```xml
<register name="CR1" extend="true">
  <exclude field="OBSOLETE_BIT" />
</register>
```

## RAM Elements

For peripherals with dedicated memory regions:

```xml
<ram_elements 
    base="0xNNNNNNNN"              <!-- Required: RAM base address -->
    size="N"                       <!-- Required: Size in bytes -->
    description="..."              <!-- Required: Description -->
    access_width="8|16|32"         <!-- Optional: Required access width -->
>
  <element 
      name="NAME"                  <!-- Required: Element name -->
      size="N"                     <!-- Required: Size in bytes -->
      max_size="true"              <!-- Optional: Size is maximum, actual varies -->
      description="..."            <!-- Required: Description -->
  >
    <word index="N">               <!-- Word within element -->
      <field name="..." mask="..." description="..." />
    </word>
  </element>
</ram_elements>
```

### Example: FDCAN Message RAM
```xml
<ram_elements base="0x4000A400" size="2560" description="Message RAM">
  <element name="FILTER_STD" size="128" description="Standard ID filters">
    <word index="0">
      <field name="SFID2" mask="0x0000FFFF" description="Standard filter ID 2" />
      <field name="SFID1" mask="0xFFFF0000" description="Standard filter ID 1" />
    </word>
  </element>
</ram_elements>
```

## Complete Peripheral Example

```xml
<?xml version="1.0" encoding="UTF-8"?>
<device name="STM32G4" reference_manual="RM0440" revision="7">
  
  <peripheral name="IWDG" description="Independent watchdog">
    <instance id="1" base="0x40003000" />
    
    <register name="KR" offset="0x00" reset="0x00000000" access="wo" 
              description="Key register">
      <field name="KEY" mask="0x0000FFFF" access="wo" description="Key value">
        <enum value="0x00005555" name="Enable" description="Enable register access" />
        <enum value="0x0000AAAA" name="Reload" description="Reload counter" />
        <enum value="0x0000CCCC" name="Start" description="Start watchdog" />
      </field>
    </register>
    
    <register name="PR" offset="0x04" reset="0x00000000" description="Prescaler register">
      <field name="PR" mask="0x00000007" access="rw" description="Prescaler divider">
        <enum value="0x00000000" name="Div4" />
        <enum value="0x00000001" name="Div8" />
        <enum value="0x00000002" name="Div16" />
        <enum value="0x00000003" name="Div32" />
        <enum value="0x00000004" name="Div64" />
        <enum value="0x00000005" name="Div128" />
        <enum value="0x00000006" name="Div256" />
      </field>
    </register>
    
    <register name="RLR" offset="0x08" reset="0x00000FFF" description="Reload register">
      <field name="RL" mask="0x00000FFF" access="rw" description="Reload value" />
    </register>
    
    <register name="SR" offset="0x0C" reset="0x00000000" access="ro" 
              description="Status register">
      <field name="PVU" mask="0x00000001" access="ro" description="Prescaler update" />
      <field name="RVU" mask="0x00000002" access="ro" description="Reload update" />
      <field name="WVU" mask="0x00000004" access="ro" description="Window update" />
    </register>
    
    <register name="WINR" offset="0x10" reset="0x00000FFF" description="Window register">
      <field name="WIN" mask="0x00000FFF" access="rw" description="Window value" />
    </register>
    
  </peripheral>
  
</device>
```

## Validation

All `.periph` files are valid XML and can be validated with:
```bash
xmllint --noout *.periph
```

## Design Decisions

1. **Mask-based fields** - Masks are used instead of bit position/width for clarity and to handle non-contiguous fields

2. **Hex values** - All addresses, offsets, masks, and reset values use lowercase hex with 0x prefix

3. **Inheritance** - Complex peripherals (TIM, USART) use inheritance to avoid duplication while capturing variant differences

4. **Groups** - Register arrays use `<group>` with stride instead of repeating register definitions

5. **Product categories** - Fields/registers available only on certain STM32G4 variants are tagged with `prodcategory`
