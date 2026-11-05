// Microphone server. An ESP32 with an INMP441 streams 16-bit PCM over TCP; this saves it as WAV, keeps a queue of
// unheard recordings for the board's speaker, serves a small web page on 127.0.0.1 and bridges to Telegram.
//
//	board -> server frames: 1 byte type, 2 bytes length (LE), payload
//	  type 1 = PCM audio, 2 = record button pressed, 4 = released (hold to record), 3 = play-next button, 5 = debug text
//	server -> board: 'R' start, 'S' stop, 'U'+count (unheard), 'N' nothing to play, 'P'+len(4, LE)+PCM
//
// Files: main.go (wiring) · board.go (connection with the board, recording) · queue.go (unheard recordings) ·
// wav.go · web.go + web/index.html (page on 127.0.0.1) · telegram.go (Telegram bridge).
package main

import (
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

type server struct {
	recDir   string
	gain     float64
	autoGain bool // no MIC_GAIN set: the loudness is adjusted automatically for every recording

	mu                      sync.Mutex
	connected               bool
	peer                    string
	rate                    uint32
	adpcm                   bool           // the connected board speaks IMA ADPCM (a quarter of the PCM traffic)
	tele                    map[string]any // last telemetry frame of the board
	teleAt, connectedAt     time.Time
	lastGone                time.Time
	connects                int // board connections since the server started
	recSamples, releasedAt  int // samples in the running recording; where the button was let go (0 = still held)
	tailGen                 int // a press or a release cancels the pending end of the previous release
	recording               bool
	level                   int
	chimePending            bool // a new Telegram message has not been announced by the board yet (it acknowledges with frame 6)
	testMode                bool // button test: the buttons only light the page, they do not record or play
	btnRec, btnNext         bool // physical buttons held right now
	recPresses, nextPresses int
	token                   string // MIC_TOKEN: the password the board must present (empty = none, for a home network)
	sounds                  soundSettings
	fw                      string // firmware build of the connected board, as it reported itself
	otaBusy                 bool
	rssi                    int // last Wi-Fi signal of the board, dBm (0 = unknown)
	lastAudio               time.Time
	raw                     *os.File
	rawPath                 string
	started                 time.Time
	conn                    net.Conn

	wmu sync.Mutex // serialises writes to the board
	tg  *telegram
}

func main() {
	loadDotEnv()
	exe, _ := os.Executable()
	recDir := env("MIC_REC_DIR", filepath.Join(filepath.Dir(exe), "recordings"))
	gain, err := strconv.ParseFloat(env("MIC_GAIN", "4"), 64)
	if err != nil {
		gain = 4
	}
	if err := os.MkdirAll(recDir, 0o755); err != nil {
		log.Fatal(err)
	}
	if v, err := strconv.Atoi(env("MIC_TAIL_MS", "")); err == nil && v >= 0 {
		recordTail = time.Duration(v) * time.Millisecond
	}
	if v, err := strconv.Atoi(env("MIC_MAX_SECONDS", "")); err == nil && v > 0 {
		maxRecording = time.Duration(v) * time.Second
	}
	s := &server{recDir: recDir, gain: gain, autoGain: os.Getenv("MIC_GAIN") == ""}
	s.token = os.Getenv("MIC_TOKEN")
	s.loadSettings()
	s.tg = startTelegram(s)

	audioPort, webPort := env("MIC_AUDIO_PORT", "8800"), env("MIC_WEB_PORT", "8801")
	ln, err := net.Listen("tcp", ":"+audioPort)
	if err != nil {
		log.Fatal(err)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.handleConn(c)
		}
	}()
	go s.remindUnplayed()
	log.Printf("web page: http://127.0.0.1:%s   audio port: %s   gain x%.1f   recordings: %s", webPort, audioPort, gain, recDir)
	log.Fatal(http.ListenAndServe("127.0.0.1:"+webPort, s.withAuth(s.web()))) // local only; published through a proxy as a Mini App when MIC_WEB_URL is set
}

// remindUnplayed repeats the new-message sound, quieter, every MIC_REMIND_SECONDS (5 by default, 0 = off)
// while something waits unplayed. The board skips it while recording.
func (s *server) remindUnplayed() {
	secs, err := strconv.Atoi(env("MIC_REMIND_SECONDS", "5"))
	if err != nil || secs <= 0 {
		return
	}
	for range time.Tick(time.Duration(secs) * time.Second) {
		s.mu.Lock()
		skip := !s.connected || s.recording || s.otaBusy
		s.mu.Unlock()
		if !skip && len(s.unplayed()) > 0 {
			s.send(append([]byte{'K'}, s.newMessageMsg(50)[1:]...))
		}
	}
}
