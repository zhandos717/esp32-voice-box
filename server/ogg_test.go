package main

import (
	"bytes"
	"encoding/binary"
	"math"
	"os/exec"
	"testing"
)

// oggPackets reads the packets back out of an Ogg stream, joining those split over 255-byte segments.
func oggPackets(t *testing.T, b []byte) [][]byte {
	var out [][]byte
	var cur []byte
	for len(b) >= 27 {
		if string(b[:4]) != "OggS" {
			t.Fatal("bad page")
		}
		n := int(b[26])
		lace := b[27 : 27+n]
		body := b[27+n:]
		for _, l := range lace {
			cur = append(cur, body[:l]...)
			body = body[l:]
			if l < 255 {
				out = append(out, cur)
				cur = nil
			}
		}
		b = body
	}
	return out
}

func TestOpusToOgg(t *testing.T) {
	ff := findFFmpeg()
	if ff == "" {
		t.Skip("no ffmpeg")
	}
	pcm := make([]byte, 0, 32000)
	for i := 0; i < 16000; i++ {
		pcm = binary.LittleEndian.AppendUint16(pcm, uint16(int16(8000*math.Sin(2*math.Pi*300*float64(i)/16000))))
	}
	cmd := exec.Command(ff, "-v", "error", "-f", "s16le", "-ar", "16000", "-ac", "1", "-i", "pipe:0",
		"-c:a", "libopus", "-b:a", "16k", "-frame_duration", "20", "-f", "ogg", "pipe:1")
	cmd.Stdin = bytes.NewReader(pcm)
	src, err := cmd.Output()
	if err != nil {
		t.Skip("ffmpeg without libopus")
	}
	var framed []byte
	for _, p := range oggPackets(t, src)[2:] { // skip OpusHead and OpusTags
		framed = binary.LittleEndian.AppendUint16(framed, uint16(len(p)))
		framed = append(framed, p...)
	}
	ogg, err := opusToOgg(framed, 16000)
	if err != nil {
		t.Fatal(err)
	}
	out, err := decodeToPCM(ogg)
	if err != nil {
		t.Fatal(err)
	}
	if d := len(out) - len(pcm); d < -1280 || d > 1280 {
		t.Fatalf("decoded %d bytes, want about %d", len(out), len(pcm))
	}
	if lv := rms16(out[len(out)/4 : len(out)*3/4]); lv < 4500 || lv > 7000 {
		t.Fatalf("level %d, want about 5650", lv)
	}
}

func TestOpusToOggEmpty(t *testing.T) {
	if _, err := opusToOgg([]byte{5, 0, 1}, 16000); err == nil {
		t.Fatal("a cut packet must not make a file")
	}
}

// A recording made of frame-11 packets ends up as a normal WAV.
func TestOpusRecording(t *testing.T) {
	ff := findFFmpeg()
	if ff == "" {
		t.Skip("no ffmpeg")
	}
	pcm := make([]byte, 0, 64000)
	for i := 0; i < 32000; i++ {
		pcm = binary.LittleEndian.AppendUint16(pcm, uint16(int16(6000*math.Sin(2*math.Pi*220*float64(i)/16000))))
	}
	cmd := exec.Command(ff, "-v", "error", "-f", "s16le", "-ar", "16000", "-ac", "1", "-i", "pipe:0",
		"-c:a", "libopus", "-b:a", "16k", "-frame_duration", "20", "-f", "ogg", "pipe:1")
	cmd.Stdin = bytes.NewReader(pcm)
	src, err := cmd.Output()
	if err != nil {
		t.Skip("ffmpeg without libopus")
	}
	s := &server{recDir: t.TempDir(), connected: true, opus: true, rate: 16000, gain: 1}
	if msg := s.startRecording(); msg != "" {
		t.Fatal(msg)
	}
	for _, p := range oggPackets(t, src)[2:] {
		s.onOpus(append([]byte{100}, p...))
	}
	if s.level != 3200 {
		t.Fatalf("level %d", s.level)
	}
	name := s.finishRecording()
	if name == "" {
		t.Fatal("no recording saved")
	}
}
