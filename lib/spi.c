#include "spi.h"

// Map a SPI peripheral to its DMAMUX request lines so the caller only has to pick
// the two channels. Extend as parts/instances are added.
static void spi_dma_reqs(struct SPI_Type *spi, enum DMA_REQ *rx, enum DMA_REQ *tx) {
	if (spi == &SPI1) {
		*rx = DMA_REQ_SPI1_RX, *tx = DMA_REQ_SPI1_TX;
	} else if (spi == &SPI2) {
		*rx = DMA_REQ_SPI2_RX, *tx = DMA_REQ_SPI2_TX;
	} else { // SPI3 (add SPI4+ here when needed)
		*rx = DMA_REQ_SPI3_RX, *tx = DMA_REQ_SPI3_TX;
	}
}

void spiq_init(
	struct SPIQ *q,
	struct SPI_Type *spi,
	enum SPI_CR1_BR clock_div,
	enum DMA_CHAN rx_chan,
	enum DMA_CHAN tx_chan,
	spi_slave_select_func_t *ss_func) {
	q->spi = spi;
	q->rx_chan = rx_chan;
	q->tx_chan = tx_chan;
	q->ss_func = ss_func;
	q->head = q->curr = q->tail = q->dropped = 0;

	// 8-bit master, mode 0 (CPOL=CPHA=0), at the requested baud divisor.
	spi->CR1 = 0;
	spi->CR1 = SPI_CR1_MSTR | clock_div;
	spi->CR2 = SPI_CR2_DS_Bits8 | SPI_CR2_FRXTH_Quarter | SPI_CR2_RXDMAEN | SPI_CR2_TXDMAEN;

	if (ss_func != NULL) {
		spi->CR1 |= SPI_CR1_SSM | SPI_CR1_SSI; // software-managed NSS held high
	} else {
		spi->CR2 |= SPI_CR2_SSOE; // hardware drives NSS
	}

	enum DMA_REQ rx_req, tx_req;
	spi_dma_reqs(spi, &rx_req, &tx_req);
	dma_set_mux(rx_chan, rx_req);
	dma_set_mux(tx_chan, tx_req);
}

static void startxmit(struct SPIQ *q) {
	struct SPIXmit *x = &q->elem[q->curr % SPI_QUEUE_LEN];

	if (q->ss_func != NULL) {
		q->ss_func(q->spi, x->addr, 1);
	}

	// RX armed before TX so no byte can arrive unclaimed once the clock runs.
	dma_start_rx(q->rx_chan, &q->spi->DR, x->buf, x->len);
	dma_start_tx(q->tx_chan, &q->spi->DR, x->buf, x->len);

	q->spi->CR1 |= SPI_CR1_SPE;
}

// Idle == SPI not enabled. startxmit sets SPE; the RX DMA handler clears it.
static inline int spi_idle(struct SPI_Type *spi) { return (spi->CR1 & SPI_CR1_SPE) ? 0 : 1; }

void spiq_enq_head(struct SPIQ *q) {
	q->head++;
	if (spi_idle(q->spi)) {
		startxmit(q);
	}
}

void spi_rx_dma_handler(struct SPIQ *q) {
	struct SPIXmit *x = &q->elem[q->curr % SPI_QUEUE_LEN];

	// RX completion ends the transaction; clear both channels' flags (the TX
	// channel has no NVIC line of its own, only this shared completion point).
	dma_isr(q->rx_chan);
	dma_isr(q->tx_chan);

	x->status = q->spi->SR & 0xf0; // error/overrun bits
	q->spi->CR1 &= ~SPI_CR1_SPE;

	if (q->ss_func != NULL) {
		q->ss_func(q->spi, x->addr, 0);
	}

	q->curr++;
	if (q->head != q->curr) {
		startxmit(q);
	}
}

void spi_wait(struct SPIQ *q) {
	while (q->curr == q->tail) { // nothing finished to deq yet
		if (spi_idle(q->spi) && q->head == q->curr) {
			return; // queue fully idle, nothing in flight
		}
		__asm volatile("wfi" ::: "memory");
	}
}

uint16_t spiq_xmit(struct SPIQ *q, uint16_t addr, size_t len, uint8_t *buf) {
	struct SPIXmit *x = spiq_head(q);
	if (x == NULL) {
		return 0xffff;
	}
	x->addr = addr;
	x->len = len;
	for (size_t i = 0; i < len; ++i) {
		x->buf[i] = buf[i];
	}
	spiq_enq_head(q);
	spi_wait(q);
	if (x != spiq_tail(q)) {
		return 0xfffe; // someone else drained our result; queue is misused
	}
	uint16_t r = x->status;
	for (size_t i = 0; i < len; ++i) {
		buf[i] = x->buf[i];
	}
	spiq_deq_tail(q);
	return r;
}
