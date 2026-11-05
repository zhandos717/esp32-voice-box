package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A stand-in for piper: it checks the arguments and writes a prepared WAV where --output_file points.
func TestSpeak(t *testing.T) {
	if findFFmpeg() == "" {
		t.Skip("no ffmpeg")
	}
	dir := t.TempDir()
	wav := filepath.Join(dir, "voice.wav")
	if err := writeWAV(wav, make([]byte, 22050*2), 22050); err != nil {
		t.Fatal(err)
	}
	fake := filepath.Join(dir, "piper")
	script := "#!/bin/sh\n[ \"$1\" = --model ] && [ \"$3\" = --output_file ] || exit 3\ncat >" + filepath.Join(dir, "text") + "\ncp " + wav + " \"$4\"\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PIPER", fake)
	t.Setenv("PIPER_MODEL", "ru.onnx")
	pcm, err := speak("  Привет, коробка!  ")
	if err != nil {
		t.Fatal(err)
	}
	if n := len(pcm) / 2; n < 15900 || n > 16100 { // one second, resampled to 16 kHz
		t.Fatalf("got %d samples", n)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "text")); strings.TrimSpace(string(got)) != "Привет, коробка!" {
		t.Fatalf("piper got %q", got)
	}
	t.Setenv("PIPER", "")
	if _, err := speak("x"); err != errNoTTS {
		t.Fatalf("unconfigured: %v", err)
	}
}

func TestSpeakAzure(t *testing.T) {
	var gotBody, gotKey, gotFmt string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody, gotKey, gotFmt = string(b), r.Header.Get("Ocp-Apim-Subscription-Key"), r.Header.Get("X-Microsoft-OutputFormat")
		if r.URL.Path != "/cognitiveservices/v1" {
			w.WriteHeader(404)
			return
		}
		f := filepath.Join(t.TempDir(), "a.wav")
		writeWAV(f, make([]byte, 3200), 16000)
		data, _ := os.ReadFile(f)
		w.Write(data)
	}))
	defer srv.Close()
	t.Setenv("AZURE_SPEECH_KEY", "k123")
	t.Setenv("AZURE_SPEECH_REGION", "westeurope")
	t.Setenv("AZURE_SPEECH_ENDPOINT", srv.URL)
	t.Setenv("AZURE_SPEECH_VOICE", "ru-RU-DmitryNeural")
	t.Setenv("AZURE_SPEECH_VOICE_KK", "kk-KZ-DauletNeural")
	speak("Привет, мама")
	if !strings.Contains(gotBody, `name="ru-RU-DmitryNeural"`) {
		t.Fatalf("russian text: %s", gotBody)
	}
	pcm, err := speak("Сәлем <мама> & папа")
	if err != nil {
		t.Fatal(err)
	}
	if len(pcm) != 3200 || gotKey != "k123" || gotFmt != "riff-16khz-16bit-mono-pcm" {
		t.Fatalf("pcm %d key %q fmt %q", len(pcm), gotKey, gotFmt)
	}
	if strings.Contains(gotBody, "prosody") {
		t.Fatalf("prosody without TTS_PITCH: %s", gotBody)
	}
	t.Setenv("TTS_PITCH", "+40Hz")
	if _, err := speak("x"); err != nil || !strings.Contains(gotBody, `<prosody pitch="+40Hz">x</prosody>`) {
		t.Fatalf("pitch: %v %s", err, gotBody)
	}
	t.Setenv("TTS_PITCH", "")
	speak("Сәлем <мама> & папа")
	if !strings.Contains(gotBody, `name="kk-KZ-DauletNeural"`) || !strings.Contains(gotBody, `xml:lang="kk-KZ"`) || !strings.Contains(gotBody, "&lt;мама&gt; &amp; папа") {
		t.Fatalf("ssml %s", gotBody)
	}
	t.Setenv("AZURE_SPEECH_KEY", "bad")
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) })
	if _, err := speak("x"); err == nil || !strings.Contains(err.Error(), "401") || strings.Contains(err.Error(), "bad") {
		t.Fatalf("auth error: %v", err)
	}
}

func TestVoiceEffect(t *testing.T) {
	if findFFmpeg() == "" {
		t.Skip("no ffmpeg")
	}
	pcm := make([]byte, 32000) // one second
	for _, name := range []string{"chipmunk", "chipmunk-light", "chipmunk-high", "chipmunk-bright", "robot"} {
		out, err := voiceEffect(pcm, name)
		if err != nil || len(out) < 16000 || len(out) > 40000 {
			t.Fatalf("%s: %d bytes, %v", name, len(out), err)
		}
	}
	if out, _ := voiceEffect(pcm, ""); len(out) != len(pcm) {
		t.Fatal("no effect must keep the sound")
	}
	if _, err := voiceEffect(pcm, "dragon"); err == nil {
		t.Fatal("unknown effect accepted")
	}
}
