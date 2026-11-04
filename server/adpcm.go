package main

import "encoding/binary"

// IMA ADPCM, 4 bits per sample: a quarter of the PCM bytes. Audio travels in independent blocks
// (a lost or late block never spoils the next one):
//
//	int16 first sample, uint8 step index, uint8 n = data bytes, then n bytes = 2n samples (low nibble first).
//
// The board announces it understands this with 4 in the "bits" field of its MIC2/MIC3 header.
const adpcmBlockSamples = 256

var imaIndex = [16]int{-1, -1, -1, -1, 2, 4, 6, 8, -1, -1, -1, -1, 2, 4, 6, 8}

var imaStep = [89]int{
	7, 8, 9, 10, 11, 12, 13, 14, 16, 17, 19, 21, 23, 25, 28, 31, 34, 37, 41, 45,
	50, 55, 60, 66, 73, 80, 88, 97, 107, 118, 130, 143, 157, 173, 190, 209, 230, 253, 279, 307,
	337, 371, 408, 449, 494, 544, 598, 658, 724, 796, 876, 963, 1060, 1166, 1282, 1411, 1552, 1707, 1878, 2066,
	2272, 2499, 2749, 3024, 3327, 3660, 4026, 4428, 4871, 5358, 5894, 6484, 7132, 7845, 8630, 9493, 10442, 11487, 12635, 13899,
	15289, 16818, 18500, 20350, 22385, 24623, 27086, 29794, 32767,
}

type imaState struct{ pred, index int }

func (st *imaState) decode(code byte) int16 {
	step := imaStep[st.index]
	diff := step >> 3
	if code&4 != 0 {
		diff += step
	}
	if code&2 != 0 {
		diff += step >> 1
	}
	if code&1 != 0 {
		diff += step >> 2
	}
	if code&8 != 0 {
		st.pred -= diff
	} else {
		st.pred += diff
	}
	st.pred = min(32767, max(-32768, st.pred))
	st.index = min(88, max(0, st.index+imaIndex[code]))
	return int16(st.pred)
}

func (st *imaState) encode(s int16) byte {
	diff := int(s) - st.pred
	var code byte
	if diff < 0 {
		code = 8
		diff = -diff
	}
	step := imaStep[st.index]
	if diff >= step {
		code |= 4
		diff -= step
	}
	if diff >= step>>1 {
		code |= 2
		diff -= step >> 1
	}
	if diff >= step>>2 {
		code |= 1
	}
	st.decode(code) // keeps the encoder's prediction exactly where the decoder's will be
	return code
}

// adpcmEncode turns 16-bit little-endian PCM into a run of blocks. The index carries over between blocks,
// so the next block starts with a step that already fits the sound.
func adpcmEncode(pcm []byte) []byte {
	n := len(pcm) / 2
	out := make([]byte, 0, n/2+(n/adpcmBlockSamples+1)*4)
	index := 0
	for i := 0; i < n; i += adpcmBlockSamples {
		m := min(adpcmBlockSamples, n-i)
		m += m & 1 // an odd tail is padded with its last sample
		first := int16(binary.LittleEndian.Uint16(pcm[2*i:]))
		st := imaState{pred: int(first), index: index}
		out = append(out, byte(first), byte(uint16(first)>>8), byte(index), byte(m/2))
		for k := 0; k < m; k += 2 {
			lo := st.encode(sampleAt(pcm, i+k, n))
			hi := st.encode(sampleAt(pcm, i+k+1, n))
			out = append(out, lo|hi<<4)
		}
		index = st.index
	}
	return out
}

func sampleAt(pcm []byte, i, n int) int16 {
	return int16(binary.LittleEndian.Uint16(pcm[2*min(i, n-1):]))
}

// adpcmDecode is the reverse; a truncated block at the end is dropped.
func adpcmDecode(b []byte) []byte {
	out := make([]byte, 0, len(b)*4)
	for len(b) >= 4 {
		st := imaState{pred: int(int16(binary.LittleEndian.Uint16(b))), index: min(88, int(b[2]))}
		size := int(b[3])
		if len(b) < 4+size {
			break
		}
		for _, v := range b[4 : 4+size] {
			out = binary.LittleEndian.AppendUint16(out, uint16(st.decode(v&15)))
			out = binary.LittleEndian.AppendUint16(out, uint16(st.decode(v>>4)))
		}
		b = b[4+size:]
	}
	return out
}
