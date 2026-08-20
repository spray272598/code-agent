package auth

import (
	"crypto/rand"
	"encoding/binary"
	"sync"
	"time"
)

// Crockford base32 alphabet (no I, L, O, U) used by ULID and user-safe tokens.
const (
	b32Crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	b32UserSafe  = "23456789ABCDEFGHJKLMNPQRSTUVWXYZ"
)

var (
	ulidMu sync.Mutex
	lastMs int64
)

// NewULID returns a canonical 26-char Crockford-base32 ULID:
// 48-bit millisecond timestamp (monotonic) + 80-bit crypto randomness.
func NewULID() string {
	var b [16]byte

	ulidMu.Lock()
	ms := time.Now().UnixMilli()
	if ms <= lastMs {
		ms = lastMs + 1
	}
	lastMs = ms
	ulidMu.Unlock()

	b[0] = byte(ms >> 40)
	b[1] = byte(ms >> 32)
	b[2] = byte(ms >> 24)
	b[3] = byte(ms >> 16)
	b[4] = byte(ms >> 8)
	b[5] = byte(ms)

	if _, err := rand.Read(b[6:16]); err != nil {
		// extremely unlikely; fall back to time-derived entropy
		seed := uint64(ms) ^ binary.BigEndian.Uint64(b[6:14])
		for i := 6; i < 16; i++ {
			seed = seed*1099511628211 + 14695981039346656037
			b[i] = byte(seed >> (8 * (i % 8)))
		}
	}
	return encodeULID(b)
}

func encodeULID(src [16]byte) string {
	dst := make([]byte, 26)
	dst[0] = b32Crockford[src[0]>>3]
	dst[1] = b32Crockford[(src[0]&7)<<2|src[1]>>6]
	dst[2] = b32Crockford[(src[1]&63)>>1]
	dst[3] = b32Crockford[(src[1]&1)<<4|src[2]>>4]
	dst[4] = b32Crockford[(src[2]&15)<<1|src[3]>>7]
	dst[5] = b32Crockford[(src[3]&127)>>2]
	dst[6] = b32Crockford[(src[3]&3)<<3|src[4]>>5]
	dst[7] = b32Crockford[src[4]&31]
	dst[8] = b32Crockford[src[5]>>3]
	dst[9] = b32Crockford[(src[5]&7)<<2|src[6]>>6]
	dst[10] = b32Crockford[(src[6]&63)>>1]
	dst[11] = b32Crockford[(src[6]&1)<<4|src[7]>>4]
	dst[12] = b32Crockford[(src[7]&15)<<1|src[8]>>7]
	dst[13] = b32Crockford[(src[8]&127)>>2]
	dst[14] = b32Crockford[(src[8]&3)<<3|src[9]>>5]
	dst[15] = b32Crockford[src[9]&31]
	dst[16] = b32Crockford[src[10]>>3]
	dst[17] = b32Crockford[(src[10]&7)<<2|src[11]>>6]
	dst[18] = b32Crockford[(src[11]&63)>>1]
	dst[19] = b32Crockford[(src[11]&1)<<4|src[12]>>4]
	dst[20] = b32Crockford[(src[12]&15)<<1|src[13]>>7]
	dst[21] = b32Crockford[(src[13]&127)>>2]
	dst[22] = b32Crockford[(src[13]&3)<<3|src[14]>>5]
	dst[23] = b32Crockford[src[14]&31]
	dst[24] = b32Crockford[src[15]>>3]
	dst[25] = b32Crockford[(src[15]&7)<<2]
	return string(dst)
}

// RandomToken returns n crypto-random chars from the full Crockford alphabet.
// Suitable for device_code and verification tokens.
func RandomToken(n int) string {
	return randomFrom(b32Crockford, n)
}

// RandomUserCode returns an n-char code drawn from an ambiguity-free alphabet
// (no 0/1/I/O) for human-entered RFC8628 user codes.
func RandomUserCode(n int) string {
	return randomFrom(b32UserSafe, n)
}

func randomFrom(alphabet string, n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		for i := range buf {
			buf[i] = alphabet[0]
		}
		return string(buf)
	}
	out := make([]byte, n)
	for i := range out {
		out[i] = alphabet[int(buf[i])%len(alphabet)]
	}
	return string(out)
}
