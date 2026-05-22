package javascript

import "strings"

// Base64 VLQ encoding for source-map v3.
//
// A VLQ digit is 5 bits of value + 1 continuation bit. The lowest bit of the
// first digit is the sign bit (1 = negative). Digits are base64-encoded
// using the standard source-map alphabet.

const b64Alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

func encodeVLQ(v int) string {
	var u uint32
	if v < 0 {
		u = (uint32(-v) << 1) | 1
	} else {
		u = uint32(v) << 1
	}
	var sb strings.Builder
	for {
		digit := u & 0x1F
		u >>= 5
		if u > 0 {
			digit |= 0x20 // continuation
		}
		sb.WriteByte(b64Alphabet[digit])
		if u == 0 {
			break
		}
	}
	return sb.String()
}
