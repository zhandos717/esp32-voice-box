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
	out := s.conditionVoice(in, 16000, 0)
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
	co := s.conditionVoice(click, 16000, 0)
	if a, b := rms(co[(prerollSamples+40)*2:(prerollSamples+200)*2]), rms(co[(prerollSamples+4000)*2:(prerollSamples+4160)*2]); a > b {
		t.Errorf("the click was not suppressed: %.0f in the click vs %.0f in the speech", a, b)
	}
	// 40 Hz rumble must be reduced strongly
	rumble := pcm(func(i int) float64 { return 3000 * math.Sin(2*math.Pi*40*float64(i)/16000) }, 16000)
	s.autoGain = false
	s.gain = 1
	if r := rms(s.conditionVoice(rumble, 16000, 0)); r > 0.15*rms(rumble) {
		t.Errorf("rumble not removed: %.0f of %.0f", r, rms(rumble))
	}
}

func TestTrimSilenceAfterRelease(t *testing.T) {
	rate := 16000
	x := make([]float64, 5*rate) // 2 s of speech, released at 2 s, a word at 2.5-3 s, then 2 s of quiet
	for i := range x {
		x[i] = 5 * math.Sin(float64(i))
		if i < 2*rate || (i >= 2*rate+rate/2 && i < 3*rate) {
			x[i] = 8000 * math.Sin(2*math.Pi*300*float64(i)/float64(rate))
		}
	}
	y := trimSilence(x, 2*rate, rate)
	want := 3*rate + keepSilenceMs*rate/1000
	if d := len(y) - want; d < -rate/50 || d > rate/50 {
		t.Fatalf("kept %d samples, want about %d", len(y), want)
	}
	quiet := append(append([]float64(nil), x[:2*rate]...), make([]float64, 2*rate)...)
	if got := len(trimSilence(quiet, 2*rate, rate)); got < 2*rate || got > 2*rate+keepSilenceMs*rate/1000+rate/50 {
		t.Fatalf("quiet tail: kept %d", got)
	}
}

func TestTrimSilenceTinyRate(t *testing.T) {
	x := make([]float64, 100)
	if len(trimSilence(x, 10, 40)) != 100 { // frame would be 0: must return, not loop
		t.Fatal("changed")
	}
}

func TestDuckEarlyClick(t *testing.T) {
	rate := 16000
	x := make([]float64, rate)
	for i := range x {
		x[i] = 50 * math.Sin(float64(i))
	}
	at := prerollSamples
	from, to := at-150*rate/1000, at-90*rate/1000 // a 60 ms click 150 ms before the reported press
	for i := from; i < to; i++ {
		x[i] = 20000 * math.Sin(2*math.Pi*900*float64(i)/float64(rate))
	}
	duckClick(x, rate)
	var peak float64
	for _, v := range x[from:to] {
		peak = math.Max(peak, math.Abs(v))
	}
	if peak > 20000*duckFloor*1.5 {
		t.Fatalf("early click kept: peak %.0f", peak)
	}
	if math.Abs(x[at+300*rate/1000]) > 60 || x[100] == 0 {
		t.Fatal("audio outside the click was changed")
	}
}
