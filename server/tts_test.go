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
	pcm, err := (*server)(nil).speak("  Привет, коробка!  ")
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
	if _, err := (*server)(nil).speak("x"); err != errNoTTS {
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
	(*server)(nil).speak("Привет, мама")
	if !strings.Contains(gotBody, `name="ru-RU-DmitryNeural"`) {
		t.Fatalf("russian text: %s", gotBody)
	}
	pcm, err := (*server)(nil).speak("Сәлем <мама> & папа")
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
	if _, err := (*server)(nil).speak("x"); err != nil || !strings.Contains(gotBody, `<prosody pitch="+40Hz">x</prosody>`) {
		t.Fatalf("pitch: %v %s", err, gotBody)
	}
	t.Setenv("TTS_PITCH", "")
	(*server)(nil).speak("Сәлем <мама> & папа")
	if !strings.Contains(gotBody, `name="kk-KZ-DauletNeural"`) || !strings.Contains(gotBody, `xml:lang="kk-KZ"`) || !strings.Contains(gotBody, "&lt;мама&gt; &amp; папа") {
		t.Fatalf("ssml %s", gotBody)
	}
	t.Setenv("AZURE_SPEECH_KEY", "bad")
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) })
	if _, err := (*server)(nil).speak("x"); err == nil || !strings.Contains(err.Error(), "401") || strings.Contains(err.Error(), "bad") {
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

func TestTTSSettingKazakh(t *testing.T) {
	t.Setenv("TTS_PITCH", "+45Hz")
	t.Setenv("TTS_EFFECT", "")
	t.Setenv("TTS_PITCH_KK", "none")
	t.Setenv("TTS_EFFECT_KK", "chipmunk")
	if ttsSetting("TTS_PITCH", false) != "+45Hz" || ttsSetting("TTS_PITCH", true) != "" {
		t.Fatal("pitch per language")
	}
	if ttsSetting("TTS_EFFECT", false) != "" || ttsSetting("TTS_EFFECT", true) != "chipmunk" {
		t.Fatal("effect per language")
	}
	t.Setenv("TTS_RATE", "+15%")
	if ttsSetting("TTS_RATE", true) != "+15%" {
		t.Fatal("no _KK value must fall back to the common one")
	}
}

// A stand-in for edge-tts: checks the arguments and copies a prepared WAV to --write-media.
func TestSpeakEdge(t *testing.T) {
	if findFFmpeg() == "" {
		t.Skip("no ffmpeg")
	}
	dir := t.TempDir()
	wav := filepath.Join(dir, "voice.wav")
	writeWAV(wav, make([]byte, 24000*2), 24000)
	args := filepath.Join(dir, "args")
	fake := filepath.Join(dir, "edge-tts")
	script := "#!/bin/sh\necho \"$@\" >" + args + "\nwhile [ $# -gt 0 ]; do [ \"$1\" = --write-media ] && cp " + wav + " \"$2\"; shift; done\n"
	os.WriteFile(fake, []byte(script), 0o755)
	t.Setenv("AZURE_SPEECH_KEY", "")
	t.Setenv("EDGE_TTS", fake)
	pcm, err := (*server)(nil).speakAs("Привет", voicePreset{Voice: "ru-RU-SvetlanaNeural", Pitch: "-25Hz", Rate: "+15%"})
	if err != nil {
		t.Fatal(err)
	}
	if n := len(pcm) / 2; n < 15900 || n > 16100 {
		t.Fatalf("got %d samples", n)
	}
	got, _ := os.ReadFile(args)
	if !strings.Contains(string(got), "--voice ru-RU-SvetlanaNeural") || !strings.Contains(string(got), "--pitch=-25Hz") || !strings.Contains(string(got), "--rate=+15%") {
		t.Fatalf("args %s", got)
	}
}
