package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ---------- queue of unheard recordings (recordings/played.json) ----------

func (s *server) playedFile() string { return filepath.Join(s.recDir, "played.json") }

func (s *server) playedSet() map[string]bool {
	set := map[string]bool{}
	data, err := os.ReadFile(s.playedFile())
	if err != nil {
		return set
	}
	var names []string
	if json.Unmarshal(data, &names) == nil {
		for _, n := range names {
			set[n] = true
		}
	}
	return set
}

func (s *server) markPlayed(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	set := s.playedSet()
	set[name] = true
	names := make([]string, 0, len(set))
	for n := range set {
		names = append(names, n)
	}
	sort.Strings(names)
	data, _ := json.Marshal(names)
	os.WriteFile(s.playedFile(), data, 0o644)
}

// rec_20261103_214006.wav / tg_20261103_215331.wav: sort by the timestamp, not by the prefix
func (s *server) allWavs() []string {
	entries, _ := os.ReadDir(s.recDir)
	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".wav") {
			names = append(names, e.Name())
		}
	}
	key := func(n string) string {
		if i := strings.Index(n, "_"); i >= 0 {
			return n[i+1:]
		}
		return n
	}
	sort.Slice(names, func(i, j int) bool { return key(names[i]) < key(names[j]) })
	return names
}

// newName returns a free file name like rec_20261103_214006.wav; a second file in the same second gets _2, _3...
// (the suffix sorts after the plain name, so the queue order stays chronological)
func (s *server) newName(prefix string) string {
	base := time.Now().Format(prefix + "_20060102_150405")
	name := base + ".wav"
	for i := 2; ; i++ {
		if _, err := os.Stat(filepath.Join(s.recDir, name)); err != nil {
			return name
		}
		name = fmt.Sprintf("%s_%d.wav", base, i)
	}
}

// unplayed is the queue of the play-next button: only messages that came from Telegram, not our own recordings
func (s *server) unplayed() []string {
	set := s.playedSet()
	var out []string
	for _, n := range s.allWavs() {
		if strings.HasPrefix(n, "tg_") && !set[n] {
			out = append(out, n)
		}
	}
	return out
}

func (s *server) unplayedMsg() []byte {
	n := len(s.unplayed())
	if n > 255 {
		n = 255
	}
	return []byte{'U', byte(n)}
}

func (s *server) sendUnplayed() { s.send(s.unplayedMsg()) }

// lastIncoming is the newest message that came from Telegram ("" if there is none).
func (s *server) lastIncoming() string {
	names := s.allWavs()
	for i := len(names) - 1; i >= 0; i-- {
		if strings.HasPrefix(names[i], "tg_") {
			return names[i]
		}
	}
	return ""
}
