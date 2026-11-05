package main

import (
	"encoding/binary"
	"math"
	"sort"
)

const (
	highPassHz     = 110.0 // below this the box and the button rumble, there is no speech
	trimTailMs     = 100   // the click of the released button is at the very end of a recording
	keepSilenceMs  = 350   // after the last sound of the tail
	prerollSamples = 6400  // the board sends this much audio from before the press first (PREROLL in the firmware): the press click follows right after it
	clickBeforeMs  = 45    // the contact is felt before the debounced press is reported
	clickAfterMs   = 80    // ringing of the button and the box after the press
	duckFloor      = 0.03  // level left in the dip
	targetLevel    = 0.62  // the loud parts of the speech go to this fraction of full scale
	maxAutoGain    = 12.0
)

// highPass is a 2nd order Butterworth high-pass filter.
func highPass(x []float64, fc, fs float64) {
	w := 2 * math.Pi * fc / fs
	cs, alpha := math.Cos(w), math.Sin(w)/(2*0.7071)
	a0 := 1 + alpha
	b0, b1, b2 := (1+cs)/2/a0, -(1+cs)/a0, (1+cs)/2/a0
	a1, a2 := -2*cs/a0, (1-alpha)/a0
	var x1, x2, y1, y2 float64
	for i, v := range x {
		y := b0*v + b1*x1 + b2*x2 - a1*y1 - a2*y2
		x2, x1, y2, y1 = x1, v, y1, y
		x[i] = y
	}
}

// loudPeak is the level below which 99.5 % of the samples lie: a click of one or two samples does not count.
func loudPeak(x []float64) float64 {
	var hist [32769]int
	for _, v := range x {
		a := int(math.Abs(v))
		if a > 32768 {
			a = 32768
		}
		hist[a]++
	}
	limit, run := int(float64(len(x))*0.005), 0
	for a := 32768; a >= 0; a-- {
		run += hist[a]
		if run > limit {
			return float64(a)
		}
	}
	return 0
}

// softLimit keeps the peaks smooth instead of cutting them flat.
func softLimit(v float64) float64 {
	const knee = 0.8 * 32767
	a := math.Abs(v)
	if a <= knee {
		return v
	}
	out := knee + (32767-knee)*math.Tanh((a-knee)/(32767-knee))
	return math.Copysign(out, v)
}

// duckClick dips the level around the moment of the button press, where the click of the button is recorded.
func duckClick(x []float64, rate int) {
	duckAt(x, prerollSamples*rate/16000, rate)
}

func duckAt(x []float64, at, rate int) {
	from, to := at-clickBeforeMs*rate/1000, at+clickAfterMs*rate/1000
	ramp := 15 * rate / 1000
	if from < 0 || to+ramp > len(x) {
		return
	}
	for i := from - ramp; i < to+ramp; i++ {
		if i < 0 {
			continue
		}
		f := duckFloor // not a hard mute, so the room noise does not jump
		if i < from {
			f = 1 - (1-duckFloor)*0.5*(1-math.Cos(math.Pi*float64(i-(from-ramp))/float64(ramp)))
		} else if i >= to {
			f = duckFloor + (1-duckFloor)*0.5*(1-math.Cos(math.Pi*float64(i-to)/float64(ramp)))
		}
		x[i] *= f
	}
}

// conditionVoice turns the raw microphone samples into a clean recording:
// no DC offset, no rumble, no click of the released button, even loudness, smooth peaks.
// conditionVoice: releasedAt is the sample where the button was let go and the tail began (0 = none).
func (s *server) conditionVoice(raw []byte, rate uint32, releasedAt int) []byte {
	n := len(raw) / 2
	x := make([]float64, n)
	var sum float64
	for i := range x {
		x[i] = float64(int16(binary.LittleEndian.Uint16(raw[2*i:])))
		sum += x[i]
	}
	mean := sum / float64(n)
	for i := range x {
		x[i] -= mean
	}
	highPass(x, highPassHz, float64(rate))
	duckClick(x, int(rate))
	if releasedAt > 0 && releasedAt < len(x) {
		duckAt(x, releasedAt, int(rate)) // the release click now sits inside the recording
		x = trimSilence(x, releasedAt, int(rate))
	} else if cut := int(rate) * trimTailMs / 1000; n > 4*cut {
		x = x[:n-cut]
	}
	g := s.gain
	if s.autoGain {
		if p := loudPeak(x); p > 1 {
			g = math.Max(1, math.Min(maxAutoGain, targetLevel*32767/p))
		} else {
			g = maxAutoGain
		}
	}
	out := make([]byte, len(x)*2)
	fade := int(rate) / 100 // 10 ms fade-in and fade-out, no click at the edges
	for i, v := range x {
		v = softLimit(v * g)
		if i < fade {
			v *= float64(i) / float64(fade)
		} else if d := len(x) - 1 - i; d < fade {
			v *= float64(d) / float64(fade)
		}
		binary.LittleEndian.PutUint16(out[2*i:], uint16(int16(v)))
	}
	return out
}

// normalize raises a quiet recording (also a voice from Telegram) before it is played on the board; it never lowers the level.
func normalize(pcm []byte) []byte {
	x := make([]float64, len(pcm)/2)
	for i := range x {
		x[i] = float64(int16(binary.LittleEndian.Uint16(pcm[2*i:])))
	}
	p := loudPeak(x)
	if p < 1 {
		return pcm
	}
	g := math.Min(maxAutoGain, 0.8*32767/p)
	if g <= 1 {
		return pcm
	}
	out := make([]byte, len(pcm))
	for i, v := range x {
		binary.LittleEndian.PutUint16(out[2*i:], uint16(int16(softLimit(v*g))))
	}
	return out
}

// trimSilence cuts the quiet end of the tail: everything after the last 20 ms frame clearly above the room noise,
// plus keepSilenceMs. Nothing before from (the release) is cut.
func trimSilence(x []float64, from, rate int) []float64 {
	frame := rate / 50
	if len(x) < 4*frame {
		return x
	}
	var levels []float64
	for i := 0; i+frame <= len(x); i += frame {
		var e float64
		for _, v := range x[i : i+frame] {
			e += v * v
		}
		levels = append(levels, math.Sqrt(e/float64(frame)))
	}
	sorted := append([]float64(nil), levels...)
	sort.Float64s(sorted)
	threshold := math.Max(sorted[len(sorted)/10]*4, 60)
	last := from / frame
	for k := len(levels) - 1; k > last; k-- {
		if levels[k] > threshold {
			last = k
			break
		}
	}
	end := (last+1)*frame + keepSilenceMs*rate/1000
	if end > len(x) {
		end = len(x)
	}
	return x[:end]
}
