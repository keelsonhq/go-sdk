package media

import (
	"crypto/rand"
	"time"
)

// crockford is the Crockford Base32 alphabet (excludes I, L, O, U).
const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// newULID generates a 26-character ULID string.
// Format: 10 chars timestamp (ms) + 16 chars random entropy (80 bits).
// Compatible with the Node/Python SDK implementations.
func newULID() string {
	millis := uint64(time.Now().UnixMilli())
	var entropy [10]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		panic("media: crypto/rand failed: " + err.Error())
	}

	var buf [26]byte
	encodeCrockford(buf[:10], millis, 10)

	// Convert 10 bytes of entropy to a single uint128-ish value via two uint64s.
	hi := uint64(entropy[0])<<8 | uint64(entropy[1])
	lo := uint64(entropy[2])<<56 | uint64(entropy[3])<<48 |
		uint64(entropy[4])<<40 | uint64(entropy[5])<<32 |
		uint64(entropy[6])<<24 | uint64(entropy[7])<<16 |
		uint64(entropy[8])<<8 | uint64(entropy[9])

	// Encode 80 bits (16 * 5 = 80) into 16 Crockford chars.
	// We process 5-bit chunks from the combined 80-bit value.
	// hi has the top 16 bits, lo has the bottom 64 bits.
	for i := 15; i >= 0; i-- {
		buf[10+i] = crockford[lo&0x1F]
		// Shift right by 5 across the two words.
		lo = (lo >> 5) | ((hi & 0x1F) << 59)
		hi >>= 5
	}

	return string(buf[:])
}

func encodeCrockford(dst []byte, value uint64, length int) {
	for i := length - 1; i >= 0; i-- {
		dst[i] = crockford[value&0x1F]
		value >>= 5
	}
}
