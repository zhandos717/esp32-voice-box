package main

import (
	"bytes"
	"encoding/binary"
	"os"
)

// ---------- WAV ----------

func writeWAV(path string, pcm []byte, rate uint32) error {
	var b bytes.Buffer
	b.WriteString("RIFF")
	binary.Write(&b, binary.LittleEndian, uint32(36+len(pcm)))
	b.WriteString("WAVEfmt ")
	binary.Write(&b, binary.LittleEndian, uint32(16))
	binary.Write(&b, binary.LittleEndian, uint16(1)) // PCM
	binary.Write(&b, binary.LittleEndian, uint16(1)) // mono
	binary.Write(&b, binary.LittleEndian, rate)
	binary.Write(&b, binary.LittleEndian, rate*2)
	binary.Write(&b, binary.LittleEndian, uint16(2))
	binary.Write(&b, binary.LittleEndian, uint16(16))
	b.WriteString("data")
	binary.Write(&b, binary.LittleEndian, uint32(len(pcm)))
	b.Write(pcm)
	return os.WriteFile(path, b.Bytes(), 0o644)
}

// wavPCM returns the samples of a mono 16-bit 16 kHz WAV file.
func wavPCM(data []byte) ([]byte, bool) {
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return nil, false
	}
	var ch, bits uint16
	var rate uint32
	pos := 12
	for pos+8 <= len(data) {
		id := string(data[pos : pos+4])
		size := int(binary.LittleEndian.Uint32(data[pos+4 : pos+8]))
		body := pos + 8
		if id == "fmt " && body+16 <= len(data) {
			ch = binary.LittleEndian.Uint16(data[body+2:])
			rate = binary.LittleEndian.Uint32(data[body+4:])
			bits = binary.LittleEndian.Uint16(data[body+14:])
		}
		if id == "data" {
			end := body + size
			if end > len(data) {
				end = len(data)
			}
			if ch == 1 && bits == 16 && rate == 16000 {
				return data[body:end], true
			}
			return nil, false
		}
		pos = body + size + size%2
	}
	return nil, false
}
