package main

import (
	"encoding/json"
	"os"
	"path/filepath"
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
	Notify int `json:"notify"`
	Boot   int `json:"boot"`
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
