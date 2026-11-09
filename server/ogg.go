package main

import (
	"encoding/binary"
	"errors"
)

// The board sends bare Opus packets (frame 11); ffmpeg needs them in an Ogg container.
// The raw recording file keeps them as uint16 LE length + packet.

const opusPacketSamples = 960 // 20 ms in Ogg's 48 kHz granule units

var oggCRC = func() (t [256]uint32) {
	for i := range t {
		r := uint32(i) << 24
		for k := 0; k < 8; k++ {
			if r&0x80000000 != 0 {
				r = r<<1 ^ 0x04c11db7
			} else {
				r <<= 1
			}
		}
		t[i] = r
	}
	return
}()

func oggPage(out []byte, flags byte, granule uint64, seq uint32, packet []byte) []byte {
	start := len(out)
	out = append(out, "OggS"...)
	out = append(out, 0, flags)
	out = binary.LittleEndian.AppendUint64(out, granule)
	out = binary.LittleEndian.AppendUint32(out, 0x4d494332) // stream serial: any constant
	out = binary.LittleEndian.AppendUint32(out, seq)
	out = append(out, 0, 0, 0, 0) // CRC, filled in below
	n := len(packet)/255 + 1
	out = append(out, byte(n))
	for i := 0; i < n-1; i++ {
		out = append(out, 255)
	}
	out = append(out, byte(len(packet)%255))
	out = append(out, packet...)
	var crc uint32
	for _, b := range out[start:] {
		crc = crc<<8 ^ oggCRC[byte(crc>>24)^b]
	}
	binary.LittleEndian.PutUint32(out[start+22:], crc)
	return out
}

// opusToOgg wraps length-prefixed Opus packets into an Ogg Opus file, one packet per page.
func opusToOgg(framed []byte, rate uint32) ([]byte, error) {
	var packets [][]byte
	for len(framed) >= 2 {
		n := int(binary.LittleEndian.Uint16(framed))
		if len(framed) < 2+n {
			break
		}
		packets = append(packets, framed[2:2+n])
		framed = framed[2+n:]
	}
	if len(packets) == 0 {
		return nil, errors.New("no Opus packets")
	}
	head := []byte("OpusHead")
	head = append(head, 1, 1, 0, 0) // version, mono, pre-skip 0
	head = binary.LittleEndian.AppendUint32(head, rate)
	head = append(head, 0, 0, 0) // gain, mapping family 0
	tags := []byte("OpusTags")
	tags = binary.LittleEndian.AppendUint32(tags, 9)
	tags = append(tags, "micserver"...)
	tags = binary.LittleEndian.AppendUint32(tags, 0)
	out := oggPage(nil, 2, 0, 0, head)
	out = oggPage(out, 0, 0, 1, tags)
	for i, p := range packets {
		var flags byte
		if i == len(packets)-1 {
			flags = 4
		}
		out = oggPage(out, flags, uint64(i+1)*opusPacketSamples, uint32(i+2), p)
	}
	return out, nil
}
