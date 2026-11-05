package main

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Text messages are read aloud. Azure AI Speech (AZURE_SPEECH_KEY, AZURE_SPEECH_REGION, AZURE_SPEECH_VOICE) wins;
// otherwise Piper (github.com/rhasspy/piper) offline on this machine: PIPER = the program, PIPER_MODEL = the voice .onnx.
// TTS_PITCH / TTS_RATE (Azure only, e.g. +40Hz, -15%) and TTS_EFFECT (chipmunk, robot) make a cartoon voice.
const maxTTSRunes = 1000

var errNoTTS = errors.New("синтез речи не настроен (AZURE_SPEECH_KEY или PIPER в .env)")

func azureConfigured() bool {
	return env("AZURE_SPEECH_KEY", "") != "" && env("AZURE_SPEECH_REGION", "") != ""
}
func piperConfigured() bool { return env("PIPER", "") != "" && env("PIPER_MODEL", "") != "" }
func ttsConfigured() bool   { return azureConfigured() || piperConfigured() }

// speak turns text into 16 kHz mono PCM, the same as a decoded voice message.
func speak(text string) ([]byte, error) {
	text = strings.TrimSpace(text)
	if r := []rune(text); len(r) > maxTTSRunes {
		text = string(r[:maxTTSRunes])
	}
	var pcm []byte
	var err error
	switch {
	case azureConfigured():
		pcm, err = speakAzure(text)
	case piperConfigured():
		pcm, err = speakPiper(text)
	default:
		return nil, errNoTTS
	}
	if err != nil {
		return nil, err
	}
	return voiceEffect(pcm, env("TTS_EFFECT", ""))
}

var voiceEffects = map[string]string{
	"chipmunk":        "asetrate=16000*2.0,aresample=16000,atempo=0.72,equalizer=f=2500:t=q:w=1:g=4", // an octave up like tape, slowed to stay clear, a nasal lift
	"chipmunk-light":  "asetrate=16000*1.45,aresample=16000,atempo=0.85",
	"chipmunk-high":   "asetrate=16000*1.8,aresample=16000,atempo=0.8",
	"chipmunk-bright": "asetrate=16000*1.65,aresample=16000,atempo=0.95,equalizer=f=3000:t=q:w=1.2:g=5,highpass=f=250",
	"robot":           "asetrate=16000*0.9,aresample=16000,aecho=0.8:0.85:12:0.4",
}

// voiceEffect runs 16 kHz PCM through an ffmpeg filter; an empty name leaves it as it is.
func voiceEffect(pcm []byte, name string) ([]byte, error) {
	if name == "" || name == "none" {
		return pcm, nil
	}
	filter, ok := voiceEffects[name]
	if !ok {
		return nil, fmt.Errorf("неизвестный TTS_EFFECT %q (есть: chipmunk, chipmunk-light, chipmunk-high, chipmunk-bright, robot)", name)
	}
	ff := findFFmpeg()
	if ff == "" {
		return nil, errors.New("для TTS_EFFECT нужен ffmpeg")
	}
	cmd := exec.Command(ff, "-v", "error", "-f", "s16le", "-ar", "16000", "-ac", "1", "-i", "pipe:0", "-af", filter, "-f", "s16le", "-ar", "16000", "-ac", "1", "pipe:1")
	cmd.Stdin = bytes.NewReader(pcm)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil || out.Len() == 0 {
		return nil, errors.New("ffmpeg не смог наложить эффект")
	}
	return out.Bytes(), nil
}

// speakAzure: REST text-to-speech; the requested format is already 16 kHz 16-bit mono WAV.
func speakAzure(text string) ([]byte, error) {
	voice := env("AZURE_SPEECH_VOICE", "ru-RU-SvetlanaNeural")
	if isKazakh(text) {
		voice = env("AZURE_SPEECH_VOICE_KK", "kk-KZ-AigulNeural")
	}
	lang := voice
	if parts := strings.SplitN(voice, "-", 3); len(parts) == 3 {
		lang = parts[0] + "-" + parts[1]
	}
	var esc bytes.Buffer
	xml.EscapeText(&esc, []byte(text))
	inner := esc.String()
	if pitch, rate := env("TTS_PITCH", ""), env("TTS_RATE", ""); pitch != "" || rate != "" {
		attr := func(k, v string) string {
			if v == "" {
				return ""
			}
			var b bytes.Buffer
			xml.EscapeText(&b, []byte(v))
			return " " + k + `="` + b.String() + `"`
		}
		inner = "<prosody" + attr("pitch", pitch) + attr("rate", rate) + ">" + inner + "</prosody>"
	}
	ssml := `<speak version="1.0" xml:lang="` + lang + `"><voice name="` + voice + `">` + inner + `</voice></speak>`
	url := env("AZURE_SPEECH_ENDPOINT", "https://"+env("AZURE_SPEECH_REGION", "")+".tts.speech.microsoft.com") + "/cognitiveservices/v1"
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(ssml))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Ocp-Apim-Subscription-Key", env("AZURE_SPEECH_KEY", ""))
	req.Header.Set("Content-Type", "application/ssml+xml")
	req.Header.Set("X-Microsoft-OutputFormat", "riff-16khz-16bit-mono-pcm")
	req.Header.Set("User-Agent", "esp32-voice-box")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("azure: нет связи") // the error text could carry the request, keep the key out of logs
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("azure: HTTP %d", resp.StatusCode)
	}
	pcm, ok := wavPCM(body)
	if !ok {
		return nil, fmt.Errorf("azure: ответ не WAV")
	}
	return pcm, nil
}

func speakPiper(text string) ([]byte, error) {
	out, err := os.CreateTemp("", "tts-*.wav")
	if err != nil {
		return nil, err
	}
	out.Close()
	defer os.Remove(out.Name())
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, env("PIPER", ""), "--model", env("PIPER_MODEL", ""), "--output_file", out.Name())
	cmd.Stdin = strings.NewReader(text)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("piper: %v %s", err, strings.TrimSpace(lastLine(stderr.String())))
	}
	wav, err := os.ReadFile(out.Name())
	if err != nil {
		return nil, err
	}
	return decodeToPCM(wav) // resampled to 16 kHz by ffmpeg
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return s[i+1:]
	}
	return s
}

// isKazakh: letters of the Kazakh Cyrillic alphabet that Russian lacks.
func isKazakh(text string) bool {
	return strings.ContainsAny(text, "әғқңөұүһіӘҒҚҢӨҰҮҺІ")
}
