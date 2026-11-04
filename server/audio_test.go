package main

import (
	"encoding/binary"
	"math"
	"testing"
)

func pcm(f func(i int) float64, n int) []byte {
	b := make([]byte, n*2)
	for i := 0; i < n; i++ {
		binary.LittleEndian.PutUint16(b[2*i:], uint16(int16(f(i))))
	}
	return b
}

func rms(b []byte) float64 {
	var s float64
	n := len(b) / 2
	for i := 0; i < n; i++ {
		v := float64(int16(binary.LittleEndian.Uint16(b[2*i:])))
		s += v * v
	}
	return math.Sqrt(s / float64(n))
}

func TestConditionVoice(t *testing.T) {
	s := &server{gain: 4, autoGain: true}
	// 1 s of a 440 Hz tone at a low level on a DC offset, with a one-sample click
	in := pcm(func(i int) float64 {
		v := 500 + 400*math.Sin(2*math.Pi*440*float64(i)/16000)
		if i == 8000 {
			v = 32767
		}
		return v
	}, 16000)
	out := s.conditionVoice(in, 16000)
	if r := rms(out); r < 3000 {
		t.Errorf("quiet tone was not raised enough: rms %.0f", r)
	}
	// a button click at the press moment is dipped, speech after it is kept
	click := pcm(func(i int) float64 {
		if i >= prerollSamples && i < prerollSamples+300 {
			return 20000 * math.Sin(float64(i)*0.9)
		}
		return 1000 * math.Sin(2*math.Pi*300*float64(i)/16000)
	}, 16000)
	s.autoGain, s.gain = false, 1
	co := s.conditionVoice(click, 16000)
	if a, b := rms(co[(prerollSamples+40)*2:(prerollSamples+200)*2]), rms(co[(prerollSamples+4000)*2:(prerollSamples+4160)*2]); a > b {
		t.Errorf("the click was not suppressed: %.0f in the click vs %.0f in the speech", a, b)
	}
	// 40 Hz rumble must be reduced strongly
	rumble := pcm(func(i int) float64 { return 3000 * math.Sin(2*math.Pi*40*float64(i)/16000) }, 16000)
	s.autoGain = false
	s.gain = 1
	if r := rms(s.conditionVoice(rumble, 16000)); r > 0.15*rms(rumble) {
		t.Errorf("rumble not removed: %.0f of %.0f", r, rms(rumble))
	}
}
