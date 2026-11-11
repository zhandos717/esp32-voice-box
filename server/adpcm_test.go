package main

import (
	"encoding/binary"
	"math"
	"testing"
)

func TestADPCMRoundTrip(t *testing.T) {
	const n = 16000 + 77 // not a whole number of blocks, odd tail
	pcm := make([]byte, 2*n)
	for i := 0; i < n; i++ {
		v := 9000*math.Sin(2*math.Pi*440*float64(i)/16000) + 3000*math.Sin(2*math.Pi*1700*float64(i)/16000)
		binary.LittleEndian.PutUint16(pcm[2*i:], uint16(int16(v)))
	}
	enc := adpcmEncode(pcm)
	if len(enc) > len(pcm)/3 {
		t.Fatalf("compressed %d of %d bytes", len(enc), len(pcm))
	}
	dec := adpcmDecode(enc)
	if len(dec) < len(pcm) || len(dec) > len(pcm)+2 {
		t.Fatalf("decoded %d bytes, want %d", len(dec), len(pcm))
	}
	var sig, noise float64
	for i := 0; i < n; i++ {
		a := float64(int16(binary.LittleEndian.Uint16(pcm[2*i:])))
		b := float64(int16(binary.LittleEndian.Uint16(dec[2*i:])))
		sig += a * a
		noise += (a - b) * (a - b)
	}
	if snr := 10 * math.Log10(sig/noise); snr < 20 {
		t.Fatalf("SNR %.1f dB", snr)
	}
}

func TestADPCMTruncated(t *testing.T) {
	enc := adpcmEncode(make([]byte, 2*adpcmBlockSamples*2))
	if got := len(adpcmDecode(enc[:len(enc)-1])); got != 2*adpcmBlockSamples {
		t.Fatalf("truncated stream decoded to %d bytes", got)
	}
}
