/*
 * device_dump.c — deterministic backend-level vector dumper.
 *
 * Drives cordic_backend_run with a fixed, seed-deterministic vector set
 * covering every FUNC/SCALE combination the frontend uses, and emits one
 * hex record per operation:
 *
 *     CSR ARG1 ARG2 RES1 RES2
 *
 * Build it twice:
 *   host:   linked against math_emul.c  -> vectors_emul.txt
 *   device: linked against math_stm32.c -> capture the UART/SWO output
 * then `diff` the two. A clean diff proves the emulation is bit-exact
 * against silicon for the exercised operating points; any mismatch is
 * localized to a FUNC/SCALE/argument and resolved via the knobs in
 * the calibration enum in math_emul.c (see README, "Calibration").
 *
 * On the device, provide putchar-level output by defining
 * cm_dump_putc(), or rely on a retargeted printf and the default below.
 */

#include <stdint.h>
#include "cordic_port.h" // adapted: was ../src/cordic_port.h (now on the -I path)
#include "tprintf.h"     // adapted: was <stdio.h>; output via the tiny printf

#ifndef CM_DUMP_PRINTF
#define CM_DUMP_PRINTF tprintf // adapted: was printf
#endif

static uint32_t st = 0x5EED5EEDu;

static uint32_t xs32(void)
{
    uint32_t x = st;
    x ^= x << 13; x ^= x >> 17; x ^= x << 5;
    return st = x;
}

/* random q1.31 in [lo, hi] (floats only used host-independently here:
 * generation is pure integer to keep the vector set platform-exact) */
static int32_t rq31(int32_t lo, int32_t hi)
{
    /* Combine in unsigned/64-bit so the full-range case rq31(INT32_MIN,
     * INT32_MAX) is platform-exact. The old `lo + (int32_t)offset` signed
     * combine overflowed/resolved differently on the device (arm-gcc) vs the
     * host, desyncing the vector set on the sin/cos and atan full-range draws. */
    uint64_t span = (uint64_t)((int64_t)hi - lo);
    uint32_t off  = (uint32_t)(((uint64_t)xs32() * (span + 1)) >> 32);
    return (int32_t)((uint32_t)lo + off);
}

static void run1(uint32_t csr, int32_t a1, int32_t a2)
{
    int32_t args[2], res[2] = { 0, 0 };
    args[0] = a1;
    args[1] = a2;
    cordic_backend_run(csr, args, res);
    CM_DUMP_PRINTF("%08X %08X %08X %08X %08X\n",
                   (unsigned)csr, (unsigned)a1, (unsigned)a2,
                   (unsigned)res[0], (unsigned)res[1]);
}

static inline int32_t Q31(double d)            /* host-side gen only */
{
    return (int32_t)(d * 2147483648.0);
}

int cm_dump_main(void)
{
    int i;

    st = 0x5EED5EEDu;

    /* sin/cos: full angle range, m = 1 and random m */
    for (i = 0; i < 400; i++) {
        int32_t th = rq31(INT32_MIN, INT32_MAX);
        run1(cm_csr(CM_FUNC_SIN, CM_PREC_TRIG, 0, 1, 1), th, INT32_MAX);
        run1(cm_csr(CM_FUNC_COS, CM_PREC_TRIG, 0, 1, 1), th,
             rq31(0, INT32_MAX));
    }

    /* phase/modulus: frontend window |a| in [0.25, 0.5), all quadrants */
    for (i = 0; i < 400; i++) {
        int32_t x = rq31(Q31(-0.5), Q31(0.5));
        int32_t y = rq31(Q31(-0.5), Q31(0.5));
        run1(cm_csr(CM_FUNC_PHASE, CM_PREC_TRIG, 0, 1, 0), x, y);
        run1(cm_csr(CM_FUNC_MOD,   CM_PREC_TRIG, 0, 1, 0), x, y);
    }
    /* phase wrap behavior near +-pi */
    for (i = 0; i < 64; i++) {
        run1(cm_csr(CM_FUNC_PHASE, CM_PREC_TRIG, 0, 1, 0),
             rq31(Q31(-0.5), Q31(-0.25)), rq31(-256, 256));
    }

    /* sqrt: both frontend windows */
    for (i = 0; i < 256; i++) {
        run1(cm_csr(CM_FUNC_SQRT, CM_PREC_SQRT, 0, 0, 0),
             rq31(Q31(0.25), Q31(0.75) - 1), 0);
        run1(cm_csr(CM_FUNC_SQRT, CM_PREC_SQRT, 1, 0, 0),
             rq31(Q31(0.375), Q31(0.5) - 1), 0);
    }

    /* ln: frontend window ARG1 in [0.25, 0.5), SCALE = 1 */
    for (i = 0; i < 256; i++) {
        run1(cm_csr(CM_FUNC_LN, CM_PREC_HYP, 1, 0, 0),
             rq31(Q31(0.25), Q31(0.5) - 1), 0);
    }

    /* cosh (expf path): ARG1 = r/2, |r| <= ln2/2 */
    for (i = 0; i < 256; i++) {
        run1(cm_csr(CM_FUNC_COSH, CM_PREC_HYP, 1, 0, 1),
             rq31(Q31(-0.1733), Q31(0.1733)), 0);
    }

    /* coverage beyond the frontend's windows (full documented ranges) */
    for (i = 0; i < 128; i++) {
        run1(cm_csr(CM_FUNC_ATAN, CM_PREC_TRIG, (xs32() % 8), 0, 0),
             rq31(INT32_MIN, INT32_MAX), 0);
        run1(cm_csr(CM_FUNC_ATANH, CM_PREC_HYP, 1, 0, 0),
             rq31(Q31(-0.403), Q31(0.403)), 0);
        run1(cm_csr(CM_FUNC_SINH, CM_PREC_HYP, 1, 0, 1),
             rq31(Q31(-0.559), Q31(0.559)), 0);
    }
    /* ln/sqrt full documented scale windows */
    for (i = 0; i < 64; i++) {
        run1(cm_csr(CM_FUNC_LN, CM_PREC_HYP, 1, 0, 0), rq31(Q31(0.0535), Q31(0.5) - 1), 0);
        run1(cm_csr(CM_FUNC_LN, CM_PREC_HYP, 2, 0, 0), rq31(Q31(0.25),  Q31(0.75) - 1), 0);
        run1(cm_csr(CM_FUNC_LN, CM_PREC_HYP, 3, 0, 0), rq31(Q31(0.375), Q31(0.875) - 1), 0);
        run1(cm_csr(CM_FUNC_LN, CM_PREC_HYP, 4, 0, 0), rq31(Q31(0.4375), Q31(0.584)), 0);
        run1(cm_csr(CM_FUNC_SQRT, CM_PREC_SQRT, 0, 0, 0), rq31(Q31(0.027), Q31(0.75) - 1), 0);
        run1(cm_csr(CM_FUNC_SQRT, CM_PREC_SQRT, 2, 0, 0), rq31(Q31(0.4375), Q31(0.585)), 0);
    }

    /* precision sweep at a few pinned arguments */
    {
        static const int32_t pins[] = {
            0x10000000, 0x3C000000, 0x7FFFFFFF, (int32_t)0x80000000, 0x00000100,
        };
        unsigned p;
        for (p = 1; p <= 15; p++) {
            for (i = 0; i < (int)(sizeof pins / sizeof pins[0]); i++) {
                run1(cm_csr(CM_FUNC_SIN, p, 0, 1, 1), pins[i], INT32_MAX);
            }
        }
    }

    return 0;
}

/* Diagnostic: precision sweep. For each of N random (angle, modulus) pairs,
 * run sin AND cos at PRECISION 1..6 (= 4,8,12,16,20,24 iterations). Lets the
 * host find the exact iteration at which the software model and the silicon
 * first diverge. Same seed/PRNG as cm_dump_main so the host can reproduce the
 * inputs exactly. */
void cm_dump_psweep(void)
{
    int n;
    unsigned p;

    st = 0x5EED5EEDu;
    for (n = 0; n < 64; n++) {
        int32_t th = rq31(INT32_MIN, INT32_MAX);
        int32_t m  = rq31(0, INT32_MAX);
        for (p = 1; p <= 6; p++) {
            run1(cm_csr(CM_FUNC_SIN, p, 0, 1, 1), th, m);
            run1(cm_csr(CM_FUNC_COS, p, 0, 1, 1), th, m);
        }
    }
}

#ifndef CM_DUMP_NO_MAIN
int main(void) { return cm_dump_main(); }
#endif
