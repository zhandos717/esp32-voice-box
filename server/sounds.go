package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
)

// The sounds live in the firmware (mic_stream.ino, playSound); here are their names and the chosen ones.
type soundInfo struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

var notifySounds = []soundInfo{
	{1, "Капля"}, {2, "«Ой-ой» (как в аське)"}, {3, "Колокольчик"}, {4, "Три ноты вверх"}, {5, "Дин-дон"},
	{6, "Мягкий пинг"}, {7, "Сонар"}, {8, "Рация «бип-бип»"}, {9, "Мягкие ноты"}, {10, "Аркада"},
}

// start-up sounds: 0 = silence; the firmware plays 100 + id
var bootSounds = []soundInfo{
	{0, "Без звука"}, {1, "PlayStation"}, {2, "Мягкое включение"}, {3, "Короткий пик"}, {4, "Две ноты вверх"},
}

type soundSettings struct {
	Notify  int                     `json:"notify"`
	Boot    int                     `json:"boot"`
	VoiceRu string                  `json:"voiceRu,omitempty"` // voice preset for text messages; empty = the .env settings
	VoiceKk string                  `json:"voiceKk,omitempty"`
	Senders map[string]*senderVoice `json:"senders,omitempty"` // Telegram user id -> their own voice
}

// senderVoice: who wrote to the box (name as Telegram shows it) and the voice their text is read with ("" = default).
type senderVoice struct {
	Name    string `json:"name"`
	VoiceRu string `json:"voiceRu,omitempty"`
	VoiceKk string `json:"voiceKk,omitempty"`
}

// noteSender remembers a person who wrote to the box, so the web page can give them a voice.
func (s *server) noteSender(id int64, name string) {
	if id == 0 {
		return
	}
	key := strconv.FormatInt(id, 10)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sounds.Senders == nil {
		s.sounds.Senders = map[string]*senderVoice{}
	}
	if v := s.sounds.Senders[key]; v != nil && v.Name == name {
		return
	} else if v != nil {
		v.Name = name
	} else {
		s.sounds.Senders[key] = &senderVoice{Name: name}
	}
	s.saveSettings()
}

func (s *server) settingsFile() string { return filepath.Join(s.recDir, "settings.json") }

func (s *server) loadSettings() {
	s.sounds = soundSettings{Notify: 1, Boot: 1}
	if b, err := os.ReadFile(s.settingsFile()); err == nil {
		json.Unmarshal(b, &s.sounds)
	}
}

func (s *server) saveSettings() {
	b, _ := json.Marshal(s.sounds)
	os.WriteFile(s.settingsFile(), b, 0o644)
}

func validSound(list []soundInfo, id int) bool {
	for _, x := range list {
		if x.ID == id {
			return true
		}
	}
	return false
}

// newMessageSound is what the board plays for a new message: id and loudness in percent.
func (s *server) newMessageMsg(percent byte) []byte {
	s.mu.Lock()
	id := s.sounds.Notify
	s.mu.Unlock()
	return []byte{'B', byte(id), percent}
}
