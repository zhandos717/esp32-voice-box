package main

import (
	"bytes"
	"crypto/md5"
	_ "embed"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed web/index.html
var page []byte

//go:embed web/vue.js
var vueJS []byte

// ---------- web (127.0.0.1 only) ----------

func (s *server) web() http.Handler {
	mux := http.NewServeMux()
	jsonOut := func(w http.ResponseWriter, v any) {
		w.Header().Set("content-type", "application/json; charset=utf-8")
		json.NewEncoder(w).Encode(v)
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("content-type", "text/html; charset=utf-8")
		w.Write(page)
	})
	mux.HandleFunc("/vue.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/javascript; charset=utf-8")
		w.Header().Set("cache-control", "public, max-age=604800")
		w.Write(vueJS)
	})
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) { jsonOut(w, s.status()) })
	// a push stream of the same status: the page redraws only when something has changed
	mux.HandleFunc("/api/events", func(w http.ResponseWriter, r *http.Request) {
		fl, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "no streaming", http.StatusInternalServerError)
			return
		}
		w.Header().Set("content-type", "text/event-stream")
		w.Header().Set("cache-control", "no-cache")
		w.Header().Set("x-accel-buffering", "no") // nginx would otherwise hold the events back
		tk := time.NewTicker(200 * time.Millisecond)
		defer tk.Stop()
		var last []byte
		quiet := 0
		for {
			b, _ := json.Marshal(s.status())
			if !bytes.Equal(b, last) {
				last, quiet = b, 0
				fmt.Fprintf(w, "data: %s\n\n", b)
				fl.Flush()
			} else if quiet++; quiet >= 75 {
				quiet = 0
				fmt.Fprint(w, ": ping\n\n")
				fl.Flush()
			}
			select {
			case <-r.Context().Done():
				return
			case <-tk.C:
			}
		}
	})
	mux.HandleFunc("/api/list", func(w http.ResponseWriter, r *http.Request) {
		set := s.playedSet()
		names := s.allWavs()
		limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
		if err != nil || limit <= 0 {
			limit = len(names)
		}
		out := make([]map[string]any, 0, min(limit, len(names)))
		for i := len(names) - 1; i >= 0 && len(out) < limit; i-- { // newest first
			out = append(out, map[string]any{"name": names[i], "played": set[names[i]]})
		}
		jsonOut(w, map[string]any{"items": out, "total": len(names)})
	})
	mux.HandleFunc("/api/start", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		if e := s.startRecording(); e != "" {
			jsonOut(w, map[string]any{"ok": false, "error": e})
			return
		}
		jsonOut(w, map[string]any{"ok": true})
	})
	mux.HandleFunc("/api/stop", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		name := s.finishRecording()
		var f any
		if name != "" {
			f = name
		}
		jsonOut(w, map[string]any{"ok": true, "file": f})
	})
	mux.HandleFunc("/api/test", func(w http.ResponseWriter, r *http.Request) { // button test mode on/off
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		s.mu.Lock()
		s.testMode = r.URL.Query().Get("on") == "1"
		if s.testMode {
			s.recPresses, s.nextPresses = 0, 0
		}
		s.mu.Unlock()
		jsonOut(w, map[string]any{"ok": true})
	})
	mux.HandleFunc("/api/led", func(w http.ResponseWriter, r *http.Request) { // light an LED on the board: id 0 = new (GPIO18), 1 = record (GPIO4), 2 = board (GPIO2)
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		id, err := strconv.Atoi(r.URL.Query().Get("id"))
		if err != nil || id < 0 || id > 2 {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		on := byte(0)
		if r.URL.Query().Get("on") == "1" {
			on = 1
		}
		if err := s.send([]byte{'L', byte(id), on}); err != nil {
			jsonOut(w, map[string]any{"ok": false, "error": "ESP32 не подключена"})
			return
		}
		jsonOut(w, map[string]any{"ok": true})
	})
	mux.HandleFunc("/api/ota", func(w http.ResponseWriter, r *http.Request) { // send the firmware image to the board over Wi-Fi
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		path := env("MIC_FIRMWARE", filepath.Join("firmware", "build", "mic_stream.ino.bin"))
		data, err := os.ReadFile(path)
		if err != nil || len(data) < 100000 {
			jsonOut(w, map[string]any{"ok": false, "error": "нет файла прошивки: " + path + " (соберите: scripts/ota.ps1)"})
			return
		}
		s.mu.Lock()
		c, busy := s.conn, s.recording || s.otaBusy
		s.mu.Unlock()
		if c == nil || busy {
			jsonOut(w, map[string]any{"ok": false, "error": "плата не подключена или занята записью"})
			return
		}
		sum := md5.Sum(data)
		msg := append([]byte{'O'}, binary.LittleEndian.AppendUint32(nil, uint32(len(data)))...)
		msg = append(msg, hex.EncodeToString(sum[:])...)
		msg = append(msg, data...)
		s.mu.Lock()
		s.otaBusy = true
		s.mu.Unlock()
		go func() {
			start := time.Now()
			log.Printf("ota: sending %s, %d bytes", path, len(data))
			err := s.write(c, msg)
			s.mu.Lock()
			s.otaBusy = false
			s.mu.Unlock()
			if err != nil {
				log.Println("ota: send failed:", err)
				return
			}
			log.Printf("ota: sent in %.0f s, the board checks it and restarts", time.Since(start).Seconds())
		}()
		jsonOut(w, map[string]any{"ok": true, "bytes": len(data)})
	})
	mux.HandleFunc("/api/sounds", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		cur := s.sounds
		s.mu.Unlock()
		jsonOut(w, map[string]any{"notify": notifySounds, "boot": bootSounds, "current": cur})
	})
	mux.HandleFunc("/api/sound/play", func(w http.ResponseWriter, r *http.Request) { // listen to a sound on the board
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		id, _ := strconv.Atoi(r.URL.Query().Get("id"))
		kind := r.URL.Query().Get("kind")
		code := id
		if kind == "boot" {
			if !validSound(bootSounds, id) || id == 0 {
				http.Error(w, "bad id", http.StatusBadRequest)
				return
			}
			code = 100 + id
		} else if !validSound(notifySounds, id) {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		if err := s.send([]byte{'K', byte(code), 100}); err != nil {
			jsonOut(w, map[string]any{"ok": false, "error": "ESP32 не подключена"})
			return
		}
		jsonOut(w, map[string]any{"ok": true})
	})
	mux.HandleFunc("/api/voices", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		cur := map[string]string{"ru": s.sounds.VoiceRu, "kk": s.sounds.VoiceKk}
		senders := []map[string]string{}
		for id, v := range s.sounds.Senders {
			senders = append(senders, map[string]string{"id": id, "name": v.Name, "ru": v.VoiceRu, "kk": v.VoiceKk})
		}
		s.mu.Unlock()
		sort.Slice(senders, func(i, j int) bool { return senders[i]["name"] < senders[j]["name"] })
		jsonOut(w, map[string]any{"presets": voicePresets, "current": cur, "senders": senders, "ready": ttsConfigured(), "azure": azureConfigured()})
	})
	mux.HandleFunc("/api/voice/select", func(w http.ResponseWriter, r *http.Request) { // id "" = back to the .env settings
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		kk, id := r.URL.Query().Get("lang") == "kk", r.URL.Query().Get("id")
		if _, ok := findPreset(id, kk); id != "" && !ok {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		user := r.URL.Query().Get("user") // a sender's own voice; empty = the default for everyone
		s.mu.Lock()
		target := &s.sounds.VoiceRu
		if kk {
			target = &s.sounds.VoiceKk
		}
		if user != "" {
			v := s.sounds.Senders[user]
			if v == nil {
				s.mu.Unlock()
				http.Error(w, "unknown sender", http.StatusBadRequest)
				return
			}
			target = map[bool]*string{false: &v.VoiceRu, true: &v.VoiceKk}[kk]
		}
		*target = id
		s.saveSettings()
		s.mu.Unlock()
		jsonOut(w, map[string]any{"ok": true})
	})
	mux.HandleFunc("/api/voice/test", func(w http.ResponseWriter, r *http.Request) { // say a sample phrase on the board right away
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		kk := r.URL.Query().Get("lang") == "kk"
		p := s.voiceFor(kk, 0)
		if id := r.URL.Query().Get("id"); id != "" {
			var ok bool
			if p, ok = findPreset(id, kk); !ok {
				http.Error(w, "bad id", http.StatusBadRequest)
				return
			}
		}
		text := map[bool]string{false: "Привет! Это проверка голоса коробки.", true: "Сәлем! Бұл қораптың дауысын тексеру."}[kk]
		pcm, err := s.speakAs(text, p)
		if err != nil {
			jsonOut(w, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		if err := s.send(s.playMsg(normalize(pcm))); err != nil {
			jsonOut(w, map[string]any{"ok": false, "error": "ESP32 не подключена"})
			return
		}
		jsonOut(w, map[string]any{"ok": true})
	})
	mux.HandleFunc("/api/sound/select", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		id, _ := strconv.Atoi(r.URL.Query().Get("id"))
		kind := r.URL.Query().Get("kind")
		s.mu.Lock()
		switch {
		case kind == "boot" && validSound(bootSounds, id):
			s.sounds.Boot = id
		case kind == "notify" && validSound(notifySounds, id):
			s.sounds.Notify = id
		default:
			s.mu.Unlock()
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		s.saveSettings()
		s.mu.Unlock()
		if kind == "boot" {
			s.send([]byte{'W', byte(id)}) // the board stores it, it is needed before the network is up
		}
		jsonOut(w, map[string]any{"ok": true})
	})
	mux.HandleFunc("/api/chime", func(w http.ResponseWriter, r *http.Request) { // test: play the new-message chime on the board
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		if err := s.send(s.newMessageMsg(100)); err != nil {
			jsonOut(w, map[string]any{"ok": false, "error": "ESP32 не подключена"})
			return
		}
		jsonOut(w, map[string]any{"ok": true})
	})
	mux.HandleFunc("/api/play", func(w http.ResponseWriter, r *http.Request) { // same as the "play next" button on the board
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		s.mu.Lock()
		c := s.conn
		s.mu.Unlock()
		if c == nil {
			jsonOut(w, map[string]any{"ok": false, "error": "ESP32 не подключена"})
			return
		}
		go s.playNext(c)
		jsonOut(w, map[string]any{"ok": true})
	})
	mux.HandleFunc("/api/mark/", func(w http.ResponseWriter, r *http.Request) {
		name := filepath.Base(strings.TrimPrefix(r.URL.Path, "/api/mark/"))
		for _, n := range s.allWavs() {
			if n == name && r.Method == http.MethodPost {
				s.markPlayed(name)
				s.sendUnplayed()
				jsonOut(w, map[string]any{"ok": true})
				return
			}
		}
		http.NotFound(w, r)
	})
	mux.HandleFunc("/rec/", func(w http.ResponseWriter, r *http.Request) {
		name := filepath.Base(strings.TrimPrefix(r.URL.Path, "/rec/"))
		if !strings.HasSuffix(name, ".wav") {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, filepath.Join(s.recDir, name))
	})
	return mux
}

func (s *server) status() map[string]any {
	files := len(s.allWavs())
	unplayed := len(s.unplayed())
	s.mu.Lock()
	level := s.level
	if time.Since(s.lastAudio) > time.Second {
		level = 0
	}
	secs := 0.0
	if s.recording {
		secs = time.Since(s.started).Seconds()
	}
	st := map[string]any{"connected": s.connected, "peer": s.peer, "recording": s.recording, "level": level, "seconds": secs,
		"fw": s.fw, "otaBusy": s.otaBusy, "btnRec": s.btnRec, "btnNext": s.btnNext, "recPresses": s.recPresses, "nextPresses": s.nextPresses, "test": s.testMode,
		"rssi": s.rssi, "adpcm": s.adpcm, "connects": s.connects, "tele": s.tele, "unplayed": unplayed, "files": files}
	if s.connected {
		st["connectedFor"] = int(time.Since(s.connectedAt).Seconds())
	}
	if !s.teleAt.IsZero() {
		st["teleAge"] = int(time.Since(s.teleAt).Seconds())
	}
	if !s.lastGone.IsZero() {
		st["lastGoneAgo"] = int(time.Since(s.lastGone).Seconds())
	}
	s.mu.Unlock()
	return st
}
