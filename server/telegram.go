package main

// Telegram two-way bridge: a voice message from the owner joins the queue of unheard recordings,
// a recording from the board is sent to the chat. Settings: telegram.json next to the program
//
//	{"token": "<bot token from @BotFather>", "chat_id": <your numeric chat id, or null while pairing>}
//
// While chat_id is null the bot answers any message with that chat's id and ignores everything else.
// ffmpeg (FFMPEG variable, ./ffmpeg/ffmpeg.exe or PATH) is needed for Telegram's OGG/Opus voice messages.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const maxVoiceBytes = 8 << 20

type telegram struct {
	token    string
	chatID   *int64
	threadID int64 // topic of a forum group (0 = no topic): recordings go there, only that topic is listened to
	base     string
	srv      *server
	client   *http.Client
	srcMu    sync.Mutex // guards tg_sources.json

	offMu       sync.Mutex // board online/offline notices
	offTimer    *time.Timer
	offNotified bool
}

// notify posts a service message into the paired chat (and topic)
func (t *telegram) notify(text string) {
	if t.chatID != nil {
		t.say(*t.chatID, t.threadID, text)
	}
}

// boardOffline: the board stays silent for a minute -> tell the chat (a quick reconnect, e.g. after flashing, makes no noise)
func (t *telegram) boardOffline() {
	t.offMu.Lock()
	defer t.offMu.Unlock()
	if t.offTimer != nil {
		t.offTimer.Stop()
	}
	t.offTimer = time.AfterFunc(60*time.Second, func() {
		t.srv.mu.Lock()
		on := t.srv.connected
		t.srv.mu.Unlock()
		t.offMu.Lock()
		send := !on && !t.offNotified
		if send {
			t.offNotified = true
		}
		t.offMu.Unlock()
		if send {
			t.notify("⚠️ ESP32 не на связи")
		}
	})
}

func (t *telegram) boardOnline() {
	t.offMu.Lock()
	if t.offTimer != nil {
		t.offTimer.Stop()
	}
	was := t.offNotified
	t.offNotified = false
	t.offMu.Unlock()
	if was {
		t.notify("✅ ESP32 снова на связи")
	}
}

// statusText answers /status
func (t *telegram) statusText() string {
	s := t.srv
	s.mu.Lock()
	on, rssi := s.connected, s.rssi
	s.mu.Unlock()
	if !on {
		return "ESP32: не на связи"
	}
	q := "слабый"
	if rssi >= -65 {
		q = "хороший"
	} else if rssi >= -80 {
		q = "средний"
	}
	sig := "сигнал неизвестен"
	if rssi != 0 {
		sig = fmt.Sprintf("сигнал %d дБм, %s", rssi, q)
	}
	fw := s.fw
	if fw == "" {
		fw = "неизвестна"
	}
	return fmt.Sprintf("ESP32: на связи, %s. Непрослушанных на плате: %d. Прошивка: %s", sig, len(s.unplayed()), fw)
}

// where a received voice came from, so that "heard" can be reported back to the sender
type tgSource struct {
	Chat   int64 `json:"chat"`
	Thread int64 `json:"thread"`
	Msg    int64 `json:"msg"`
	Kk     bool  `json:"kk,omitempty"`
}

func (t *telegram) sourcesFile() string { return filepath.Join(t.srv.recDir, "tg_sources.json") }

func (t *telegram) loadSources() map[string]tgSource {
	m := map[string]tgSource{}
	if b, err := os.ReadFile(t.sourcesFile()); err == nil {
		json.Unmarshal(b, &m)
	}
	return m
}

func (t *telegram) rememberSource(name string, src tgSource) {
	t.srcMu.Lock()
	defer t.srcMu.Unlock()
	m := t.loadSources()
	m[name] = src
	b, _ := json.Marshal(m)
	os.WriteFile(t.sourcesFile(), b, 0o644)
}

// heard tells the sender that the board has played their voice message: a reaction, or a reply if reactions are refused
func (t *telegram) heard(name string) {
	t.srcMu.Lock()
	src, ok := t.loadSources()[name]
	t.srcMu.Unlock()
	if !ok || src.Msg == 0 {
		return
	}
	_, err := t.api("setMessageReaction", map[string]any{
		"chat_id": src.Chat, "message_id": src.Msg,
		"reaction": []map[string]string{{"type": "emoji", "emoji": "👌"}},
	}, nil, 30*time.Second)
	if err != nil {
		log.Println("telegram: reaction failed, replying instead -", err)
		p := map[string]any{"chat_id": src.Chat, "text": tr(src.Kk, "heard"), "reply_to_message_id": src.Msg}
		if src.Thread != 0 {
			p["message_thread_id"] = src.Thread
		}
		if _, err := t.api("sendMessage", p, nil, 30*time.Second); err != nil {
			log.Println(err)
			return
		}
	}
	log.Println("telegram: reported as heard:", name)
}

type tgMedia struct {
	FileID   string `json:"file_id"`
	MimeType string `json:"mime_type"`
}

type tgMessage struct {
	Chat struct {
		ID int64 `json:"id"`
	} `json:"chat"`
	MessageID int64 `json:"message_id"`
	From      struct {
		ID        int64  `json:"id"`
		FirstName string `json:"first_name"`
		LastName  string `json:"last_name"`
		Username  string `json:"username"`
		Language  string `json:"language_code"`
	} `json:"from"`
	ThreadID  int64    `json:"message_thread_id"`
	Text      string   `json:"text"`
	Voice     *tgMedia `json:"voice"`
	Audio     *tgMedia `json:"audio"`
	VideoNote *tgMedia `json:"video_note"`
	Document  *tgMedia `json:"document"`
}

type tgUpdate struct {
	UpdateID int64      `json:"update_id"`
	Message  *tgMessage `json:"message"`
}

func findFFmpeg() string {
	exe, _ := os.Executable()
	wd, _ := os.Getwd()
	dir := filepath.Dir(exe)
	// FFMPEG variable, then ./ffmpeg next to the program, in the project root above it, or in the working directory
	for _, c := range []string{os.Getenv("FFMPEG"),
		filepath.Join(dir, "ffmpeg", "ffmpeg.exe"), filepath.Join(dir, "..", "ffmpeg", "ffmpeg.exe"),
		filepath.Join(wd, "ffmpeg", "ffmpeg.exe"), filepath.Join(dir, "ffmpeg", "ffmpeg")} {
		if c != "" {
			if st, err := os.Stat(c); err == nil && !st.IsDir() {
				return c
			}
		}
	}
	if p, err := exec.LookPath("ffmpeg"); err == nil {
		return p
	}
	return ""
}

// decodeToPCM: any audio Telegram can send -> raw 16 kHz mono int16. WAV is read directly, the rest through ffmpeg.
func decodeToPCM(data []byte) ([]byte, error) {
	if pcm, ok := wavPCM(data); ok {
		return pcm, nil
	}
	ff := findFFmpeg()
	if ff == "" {
		return nil, errors.New("нужен ffmpeg для голосовых Telegram (положите его в папку ffmpeg рядом с программой)")
	}
	cmd := exec.Command(ff, "-v", "error", "-i", "pipe:0", "-t", strconv.Itoa(int(maxRecording.Seconds())), "-ac", "1", "-ar", "16000", "-f", "s16le", "pipe:1")
	cmd.Stdin = bytes.NewReader(data)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil || out.Len() == 0 {
		return nil, errors.New("ffmpeg не смог прочитать аудио")
	}
	return out.Bytes(), nil
}

func wavToOgg(path string) []byte {
	ff := findFFmpeg()
	if ff == "" {
		return nil
	}
	var out bytes.Buffer
	cmd := exec.Command(ff, "-v", "error", "-i", path, "-c:a", "libopus", "-b:a", "24k", "-f", "ogg", "pipe:1")
	cmd.Stdout = &out
	if cmd.Run() != nil || out.Len() == 0 {
		return nil
	}
	return out.Bytes()
}

// firstEnv returns the first non-empty variable of the list.
func firstEnv(names ...string) string {
	for _, n := range names {
		if v := strings.TrimSpace(os.Getenv(n)); v != "" {
			return v
		}
	}
	return ""
}

func startTelegram(s *server) *telegram {
	var cfg struct {
		Token    string `json:"token"`
		ChatID   *int64 `json:"chat_id"`
		ThreadID int64  `json:"thread_id"`
	}
	// Settings: an explicit TELEGRAM_CONFIG file wins (the tests use it); otherwise .env / environment
	// (TELEGRAM_BOT_TOKEN, TELEGRAM_CHAT_ID), otherwise telegram.json next to the program.
	if path := os.Getenv("TELEGRAM_CONFIG"); path != "" {
		data, err := os.ReadFile(path)
		if err != nil || json.Unmarshal(data, &cfg) != nil {
			log.Println("telegram: cannot read", path)
			return nil
		}
	} else if tok := firstEnv("TELEGRAM_BOT_TOKEN", "TELEGRAM_BOT", "TELEGRAM_TOKEN"); tok != "" {
		cfg.Token = tok
		if id, err := strconv.ParseInt(strings.TrimSpace(os.Getenv("TELEGRAM_CHAT_ID")), 10, 64); err == nil {
			cfg.ChatID = &id
		}
		if id, err := strconv.ParseInt(strings.TrimSpace(os.Getenv("TELEGRAM_THREAD_ID")), 10, 64); err == nil {
			cfg.ThreadID = id
		}
	} else {
		exe, _ := os.Executable()
		data, err := os.ReadFile(filepath.Join(filepath.Dir(exe), "telegram.json"))
		if err != nil {
			log.Println("telegram: not configured (no TELEGRAM_BOT_TOKEN in .env, no telegram.json)")
			return nil
		}
		json.Unmarshal(data, &cfg)
	}
	if strings.TrimSpace(cfg.Token) == "" || strings.Contains(cfg.Token, "ВСТАВЬТЕ") {
		log.Println("telegram: no token yet")
		return nil
	}
	t := &telegram{token: strings.TrimSpace(cfg.Token), chatID: cfg.ChatID, threadID: cfg.ThreadID,
		base: strings.TrimRight(env("TELEGRAM_API_BASE", "https://api.telegram.org"), "/"), srv: s,
		client: &http.Client{Timeout: 120 * time.Second}}
	go t.run()
	go t.setMenuButton()
	return t
}

// api calls a Bot API method. Errors never contain the URL, because the token is part of it.
func (t *telegram) api(method string, params map[string]any, files map[string]fileField, timeout time.Duration) (json.RawMessage, error) {
	var body io.Reader
	ctype := "application/json"
	if len(files) > 0 {
		var b bytes.Buffer
		w := multipart.NewWriter(&b)
		for k, v := range params {
			w.WriteField(k, fmt.Sprint(v))
		}
		for k, f := range files {
			h := make(textproto.MIMEHeader)
			h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`, k, f.name))
			h.Set("Content-Type", f.ctype)
			part, _ := w.CreatePart(h)
			part.Write(f.data)
		}
		w.Close()
		body, ctype = &b, w.FormDataContentType()
	} else {
		data, _ := json.Marshal(params)
		body = bytes.NewReader(data)
	}
	req, _ := http.NewRequest("POST", fmt.Sprintf("%s/bot%s/%s", t.base, t.token, method), body)
	req.Header.Set("content-type", ctype)
	c := &http.Client{Timeout: timeout}
	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Telegram %s: network error", method)
	}
	defer resp.Body.Close()
	var out struct {
		OK          bool            `json:"ok"`
		Result      json.RawMessage `json:"result"`
		Description string          `json:"description"`
	}
	if json.NewDecoder(resp.Body).Decode(&out) != nil {
		return nil, fmt.Errorf("Telegram %s: HTTP %d", method, resp.StatusCode)
	}
	if !out.OK {
		return nil, fmt.Errorf("Telegram %s: %s", method, out.Description)
	}
	return out.Result, nil
}

type fileField struct {
	name, ctype string
	data        []byte
}

func (t *telegram) download(fileID string) ([]byte, error) {
	res, err := t.api("getFile", map[string]any{"file_id": fileID}, nil, 30*time.Second)
	if err != nil {
		return nil, err
	}
	var info struct {
		FilePath string `json:"file_path"`
	}
	json.Unmarshal(res, &info)
	resp, err := t.client.Get(fmt.Sprintf("%s/file/bot%s/%s", t.base, t.token, info.FilePath))
	if err != nil {
		return nil, errors.New("Telegram download: network error")
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, maxVoiceBytes+1))
	if len(data) > maxVoiceBytes {
		return nil, errors.New("голосовое слишком большое")
	}
	return data, nil
}

// say replies in the same chat (and the same topic, if the message came from one).
func (t *telegram) say(chat, thread int64, text string) {
	p := map[string]any{"chat_id": chat, "text": text}
	if thread != 0 {
		p["message_thread_id"] = thread
	}
	if _, err := t.api("sendMessage", p, nil, 30*time.Second); err != nil {
		log.Println(err)
	}
}

// target is where recordings from the board are posted: the paired chat and, if set, its topic.
func (t *telegram) target() map[string]any {
	p := map[string]any{"chat_id": *t.chatID}
	if t.threadID != 0 {
		p["message_thread_id"] = t.threadID
	}
	return p
}

// from the board to Telegram
func (t *telegram) sendRecording(wavPath string) {
	if t.chatID == nil {
		return
	}
	go func() {
		name := filepath.Base(wavPath)
		if ogg := wavToOgg(wavPath); ogg != nil {
			_, err := t.api("sendVoice", t.target(), map[string]fileField{"voice": {"voice.ogg", "audio/ogg", ogg}}, 120*time.Second)
			if err != nil {
				log.Println("telegram: could not send", name, "-", err)
				return
			}
		} else {
			data, err := os.ReadFile(wavPath)
			if err == nil {
				p := t.target()
				p["caption"] = "запись с платы"
				_, err = t.api("sendDocument", p, map[string]fileField{"document": {name, "audio/wav", data}}, 120*time.Second)
			}
			if err != nil {
				log.Println("telegram: could not send", name, "-", err)
				return
			}
		}
		log.Println("telegram: sent", name)
	}()
}

// from Telegram to the board
func (t *telegram) handle(m *tgMessage) {
	chat, thread := m.Chat.ID, m.ThreadID
	if t.chatID == nil { // pairing mode
		t.say(chat, thread, fmt.Sprintf("Числовой ID чата: %d\nID топика: %d (0 = без топика)\nВпишите их в .env: TELEGRAM_CHAT_ID и TELEGRAM_THREAD_ID, затем перезапустите сервер.", chat, thread))
		log.Println("telegram: pairing request from chat", chat)
		return
	}
	if chat != *t.chatID {
		return // strangers get no answer
	}
	log.Printf("telegram: message in the paired chat, topic %d", thread) // so the topic number can be read from the log
	if strings.HasPrefix(strings.TrimSpace(m.Text), "/status") {
		t.say(chat, thread, t.statusText())
		return
	}
	if strings.HasPrefix(strings.TrimSpace(m.Text), "/id") { // asked from any topic of the paired chat
		t.say(chat, thread, fmt.Sprintf("Чат: %d\nТопик (TELEGRAM_THREAD_ID): %d", chat, thread))
		return
	}
	if t.threadID != 0 && thread != t.threadID {
		return // another topic of the group: not ours
	}
	t.srv.noteSender(m.From.ID, senderName(m))
	kk := replyKazakh(m)
	media := m.Voice
	if media == nil {
		media = m.Audio
	}
	if media == nil {
		media = m.VideoNote
	}
	if media == nil && m.Document != nil && strings.HasPrefix(m.Document.MimeType, "audio") {
		media = m.Document
	}
	if media == nil {
		text := strings.TrimSpace(m.Text)
		if text == "" || strings.HasPrefix(text, "/") || !ttsConfigured() {
			t.say(chat, thread, tr(kk, "hint"))
			return
		}
		voice := t.srv.voiceFor(isKazakh(text), m.From.ID)
		pcm, err := t.srv.speakAs(text, voice)
		if err != nil {
			t.say(chat, thread, tr(kk, "ttsFail")+err.Error())
			log.Println("telegram: text to speech failed -", err)
			return
		}
		t.enqueue(chat, thread, m.MessageID, kk, pcm, "text read aloud", voice.label(kk))
		return
	}
	data, err := t.download(media.FileID)
	var pcm []byte
	if err == nil {
		pcm, err = decodeToPCM(data)
	}
	if err != nil {
		t.say(chat, thread, tr(kk, "recvFail")+err.Error())
		log.Println("telegram: voice failed -", err)
		return
	}
	t.enqueue(chat, thread, m.MessageID, kk, pcm, "voice", "")
}

// enqueue puts a message for the board into the unplayed queue and rings the board.
// A synthesised one (voiceName set) is also sent back as a voice reply, so the sender hears what the box will say.
func (t *telegram) enqueue(chat, thread, msgID int64, kk bool, pcm []byte, kind, voiceName string) {
	name := t.srv.newName("tg")
	full := filepath.Join(t.srv.recDir, name)
	if err := writeWAV(full, pcm, 16000); err != nil {
		log.Println("cannot save:", err)
		return
	}
	t.rememberSource(name, tgSource{Chat: chat, Thread: thread, Msg: msgID, Kk: kk})
	secs := float64(len(pcm)) / 32000
	log.Printf("telegram: %s saved %s %.1f s", kind, name, secs)
	t.srv.sendUnplayed()
	t.srv.mu.Lock()
	t.srv.chimePending = true
	t.srv.mu.Unlock()
	t.srv.send(t.srv.newMessageMsg(100)) // sound on the board: a new message has arrived (resent after a reconnect until the board acknowledges)
	text := fmt.Sprintf(tr(kk, "accepted"), secs)
	if voiceName != "" {
		if ogg := wavToOgg(full); ogg != nil {
			p := map[string]any{"chat_id": chat, "caption": text + tr(kk, "voice") + voiceName,
				"reply_parameters": fmt.Sprintf(`{"message_id":%d,"allow_sending_without_reply":true}`, msgID)} // form field: JSON as text
			if thread != 0 {
				p["message_thread_id"] = thread
			}
			if _, err := t.api("sendVoice", p, map[string]fileField{"voice": {"voice.ogg", "audio/ogg", ogg}}, 60*time.Second); err == nil {
				return
			} else {
				log.Println("telegram: voice reply failed -", err)
			}
		}
		text += tr(kk, "voice") + voiceName
	}
	t.say(chat, thread, text)
}

func (t *telegram) run() {
	offsetFile := filepath.Join(t.srv.recDir, ".tg_offset")
	var offset int64
	if b, err := os.ReadFile(offsetFile); err == nil {
		offset, _ = strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	}
	mode := "paired"
	if t.chatID == nil {
		mode = "pairing mode (no chat_id yet)"
	}
	log.Println("telegram: bridge started,", mode)
	for {
		res, err := t.api("getUpdates", map[string]any{"offset": offset, "timeout": 25, "allowed_updates": []string{"message"}}, nil, 40*time.Second)
		if err != nil {
			log.Println(err)
			time.Sleep(5 * time.Second)
			continue
		}
		var updates []tgUpdate
		json.Unmarshal(res, &updates)
		for _, u := range updates {
			offset = u.UpdateID + 1
			if u.Message != nil {
				t.handle(u.Message)
			}
			os.WriteFile(offsetFile, []byte(strconv.FormatInt(offset, 10)), 0o644)
		}
	}
}

func senderName(m *tgMessage) string {
	name := strings.TrimSpace(m.From.FirstName + " " + m.From.LastName)
	if name == "" && m.From.Username != "" {
		name = "@" + m.From.Username
	}
	return name
}

var botText = map[string][2]string{ // Russian, Kazakh
	"accepted": {"Принято (%.0f с): ждёт на колонке платы.", "Қабылданды (%.0f с): плата динамигінде күтіп тұр."},
	"voice":    {"\nГолос: ", "\nДауыс: "},
	"heard":    {"✅ Прослушано", "✅ Тыңдалды"},
	"hint":     {"Пришлите голосовое или текстовое сообщение: оно будет ждать на колонке платы.", "Дауыстық не мәтіндік хабар жіберіңіз: ол плата динамигінде күтіп тұрады."},
	"ttsFail":  {"Не получилось озвучить: ", "Дыбыстау мүмкін болмады: "},
	"recvFail": {"Не получилось принять: ", "Қабылдау мүмкін болмады: "},
}

func tr(kk bool, key string) string {
	if kk {
		return botText[key][1]
	}
	return botText[key][0]
}

// replyKazakh: a text answers in its own language, a voice message in the language of the sender's Telegram
func replyKazakh(m *tgMessage) bool {
	if t := strings.TrimSpace(m.Text); t != "" && !strings.HasPrefix(t, "/") {
		return isKazakh(t)
	}
	return m.From.Language == "kk"
}
