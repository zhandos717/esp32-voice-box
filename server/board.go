package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const minHold = 300 * time.Millisecond // a button hold shorter than this is an accidental tap

var maxRecording = 5 * time.Minute // MIC_MAX_SECONDS overrides it

// ---------- writing to the board ----------

func (s *server) write(c net.Conn, b []byte) error {
	if c == nil {
		return fmt.Errorf("no board")
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	_, err := c.Write(b)
	return err
}

func (s *server) send(b []byte) error {
	s.mu.Lock()
	c := s.conn
	s.mu.Unlock()
	return s.write(c, b)
}

// ---------- the board connection ----------

func rms16(b []byte) int {
	n := len(b) / 2
	if n == 0 {
		return 0
	}
	var sum float64
	for i := 0; i < n; i++ {
		v := float64(int16(binary.LittleEndian.Uint16(b[2*i:])))
		sum += v * v
	}
	return int(math.Sqrt(sum / float64(n)))
}

func (s *server) onAudio(b []byte) {
	lv := rms16(b)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastAudio = time.Now()
	s.level = lv
	if s.recording && s.raw != nil {
		s.raw.Write(b)
	}
}

func (s *server) startRecording() string {
	s.mu.Lock()
	if !s.connected {
		s.mu.Unlock()
		return "ESP32 не подключена"
	}
	if s.recording {
		s.mu.Unlock()
		return ""
	}
	path := filepath.Join(s.recDir, ".rec.raw")
	f, err := os.Create(path)
	if err != nil {
		s.mu.Unlock()
		return err.Error()
	}
	s.raw, s.rawPath, s.recording, s.started = f, path, true, time.Now()
	started := s.started
	s.mu.Unlock()
	s.send([]byte{'R'})
	log.Println("recording started")
	// safety net: a lost "button released" message must not leave the recording running for ever
	time.AfterFunc(maxRecording, func() {
		s.mu.Lock()
		same := s.recording && s.started.Equal(started)
		s.mu.Unlock()
		if same {
			log.Println("recording stopped: maximum length reached")
			s.finishRecording()
		}
	})
	return ""
}

// discardRecording throws the current recording away (an accidental tap on the button).
func (s *server) discardRecording() {
	s.mu.Lock()
	if !s.recording {
		s.mu.Unlock()
		return
	}
	s.recording = false
	raw, path := s.raw, s.rawPath
	s.raw = nil
	s.mu.Unlock()
	raw.Close()
	os.Remove(path)
	s.send([]byte{'S'})
}

func (s *server) finishRecording() string {
	s.mu.Lock()
	if !s.recording {
		s.mu.Unlock()
		return ""
	}
	s.recording = false
	raw, path, rate := s.raw, s.rawPath, s.rate
	s.raw = nil
	s.mu.Unlock()
	raw.Close()
	s.send([]byte{'S'})
	data, err := os.ReadFile(path)
	os.Remove(path)
	if err != nil || len(data) < int(rate)/5*2 {
		log.Println("recording too short, dropped")
		return ""
	}
	out := s.conditionVoice(data, rate)
	n := len(out) / 2
	name := s.newName("rec")
	full := filepath.Join(s.recDir, name)
	if err := writeWAV(full, out, rate); err != nil {
		log.Println("cannot save:", err)
		return ""
	}
	log.Printf("saved %s %.1f s", name, float64(n)/float64(rate))
	s.sendUnplayed()
	if s.tg != nil {
		s.tg.sendRecording(full)
	}
	return name
}

func (s *server) playNext(c net.Conn) {
	var name string
	replay := false // nothing new: the button plays the last received message once more
	if names := s.unplayed(); len(names) > 0 {
		name = names[0]
	} else if name = s.lastIncoming(); name != "" {
		replay = true
	} else {
		s.write(c, []byte{'N'})
		return
	}
	data, err := os.ReadFile(filepath.Join(s.recDir, name))
	pcm, ok := wavPCM(data)
	if err != nil || !ok {
		log.Println("cannot play", name, "- skipping")
		if !replay {
			s.markPlayed(name)
		}
		s.write(c, []byte{'N'})
		return
	}
	pcm = normalize(pcm)
	log.Printf("playing on the board: %s %.1f s%s", name, float64(len(pcm))/32000, map[bool]string{true: " (repeat)", false: ""}[replay])
	msg := make([]byte, 5, 5+len(pcm))
	msg[0] = 'P'
	binary.LittleEndian.PutUint32(msg[1:], uint32(len(pcm)))
	msg = append(msg, pcm...)
	if s.write(c, msg) == nil && !replay { // blocks until the board has taken it
		s.markPlayed(name)
		s.write(c, s.unplayedMsg())
		if s.tg != nil {
			go s.tg.heard(name) // tell the Telegram sender their voice message was played
		}
	}
}

func (s *server) handleConn(c net.Conn) {
	defer c.Close()
	c.SetReadDeadline(time.Now().Add(10 * time.Second))
	hdr := make([]byte, 10)
	if _, err := io.ReadFull(c, hdr); err != nil || string(hdr[:4]) != "MIC2" {
		log.Println("rejected", c.RemoteAddr(), "(bad header, old firmware?)")
		return
	}
	c.SetReadDeadline(time.Time{})
	rate := binary.LittleEndian.Uint32(hdr[4:8])
	s.mu.Lock()
	if s.conn != nil {
		s.conn.Close() // a new connection replaces the old one
	}
	s.conn, s.connected, s.peer, s.rate = c, true, c.RemoteAddr().String(), rate
	s.btnRec, s.btnNext = false, false // a new connection starts with released buttons
	s.mu.Unlock()
	log.Println("ESP32 connected", c.RemoteAddr(), rate, "Hz")
	if s.tg != nil {
		s.tg.boardOnline()
	}
	s.write(c, s.unplayedMsg())
	s.mu.Lock()
	pending, boot := s.chimePending, s.sounds.Boot
	s.mu.Unlock()
	s.write(c, []byte{'W', byte(boot)}) // the board keeps the start-up sound choice itself
	if pending {
		s.write(c, s.newMessageMsg(100)) // the sound was lost with the previous connection
	}
	defer func() {
		s.mu.Lock()
		gone := s.conn == c // false when a newer connection has already replaced this one
		if gone {
			s.conn, s.connected, s.level, s.rssi = nil, false, 0, 0
			s.btnRec, s.btnNext = false, false
		}
		s.mu.Unlock()
		log.Println("ESP32 disconnected")
		s.finishRecording()
		if gone && s.tg != nil {
			s.tg.boardOffline()
		}
	}()
	head := make([]byte, 3)
	for {
		c.SetReadDeadline(time.Now().Add(45 * time.Second)) // the board reports its Wi-Fi signal every 15 s: silence means it is gone
		if _, err := io.ReadFull(c, head); err != nil {
			return
		}
		payload := make([]byte, int(head[1])|int(head[2])<<8)
		if _, err := io.ReadFull(c, payload); err != nil {
			return
		}
		switch head[0] {
		case 1:
			s.onAudio(payload)
		case 2: // record button pressed: hold to record
			s.mu.Lock()
			s.btnRec = true
			s.recPresses++
			test := s.testMode
			s.mu.Unlock()
			if test {
				break
			}
			log.Println("button pressed")
			s.startRecording()
		case 4: // record button released
			s.mu.Lock()
			s.btnRec = false
			if s.testMode {
				s.mu.Unlock()
				break
			}
			rec, held := s.recording, time.Since(s.started)
			s.mu.Unlock()
			if rec && held < minHold {
				log.Println("button released: too short a tap, ignored")
				s.discardRecording()
			} else if rec {
				log.Printf("button released after %.1f s", held.Seconds())
				s.finishRecording()
			}
		case 3:
			s.mu.Lock()
			test := s.testMode
			s.mu.Unlock()
			if !test {
				s.playNext(c)
			}
		case 7, 8: // play-next button pressed / released (for the button test)
			s.mu.Lock()
			s.btnNext = head[0] == 7
			if head[0] == 7 {
				s.nextPresses++
			}
			s.mu.Unlock()
		case 6: // the board has played the new-message chime
			log.Println("board: chime played")
			s.mu.Lock()
			s.chimePending = false
			s.mu.Unlock()
		case 5: // debug text from the board
			log.Printf("board: %s", payload)
			if t := string(payload); strings.HasPrefix(t, "firmware ") {
				s.mu.Lock()
				s.fw = strings.TrimPrefix(t, "firmware ")
				s.mu.Unlock()
			}
			if i := strings.Index(string(payload), "Wi-Fi signal "); i >= 0 {
				var dbm int
				if _, err := fmt.Sscanf(string(payload)[i:], "Wi-Fi signal %d", &dbm); err == nil {
					s.mu.Lock()
					s.rssi = dbm
					s.mu.Unlock()
				}
			}
		}
	}
}
