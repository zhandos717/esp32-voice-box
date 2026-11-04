// ESP32 + INMP441 microphone, record button, speaker (MAX98357A) with a "play next unheard" button.
//
// Microphone INMP441:  SCK->GPIO26, WS->GPIO25, SD->GPIO33, L/R->GND, VDD->3V3, GND->GND
// Record button:       one contact to GPIO27, the other to GND   (GPIO4 = HIGH while recording, for a MOSFET/backlight)
// Play-next button:    one contact to GPIO13, the other to GND
// "New recordings" LED: GPIO18 -> 330 ohm -> LED -> GND (lit while there are unheard recordings)
// Speaker amp MAX98357A: BCLK->GPIO21, LRC->GPIO19, DIN->GPIO22, VIN->5V, GND->GND
//
// Wi-Fi name, password and the PC address are NOT in the source: run set_wifi.py on the PC once.
#include <ESP_I2S.h>
#include <Preferences.h>
#include <WiFi.h>
#include <esp_system.h>
#include <esp_task_wdt.h>
#include <esp_wifi.h>
#include <Update.h>
#include <lwip/sockets.h>
#include <lwip/netdb.h>
#include <freertos/stream_buffer.h>

#include "config.h"

// a note of a sound: start Hz, end Hz, ms, loudness, decay ms (0 = none), 2nd harmonic (declared here: the Arduino prototypes need it early)
struct Note { float f0, f1; uint16_t ms; float amp, tau, h2; };

I2SClass i2s, i2sOut;   // automatic ports: explicit ports 0 and 1 left the speaker silent (see firmware/speaker_mic_test)
Preferences prefs;
WiFiClient client;
String ssid, pass, host, token;
uint16_t port = 8800;
bool streaming = false;
int32_t raw[256];
StreamBufferHandle_t micBuf;                // 16-bit samples from the microphone task to the loop: 2 s of slack for a stalled network write

// Reads the microphone non-stop, so a slow Wi-Fi write in loop() can never make the I2S DMA overflow and lose audio
void micTask(void *) {
  static int16_t pcm16[256];
  for (;;) {
    size_t got = i2s.readBytes((char *)raw, sizeof(raw));
    int n = got / sizeof(int32_t);
    for (int i = 0; i < n; i++) pcm16[i] = raw[i] >> 16;   // top 16 of the 24 bits
    if (n > 0) xStreamBufferSend(micBuf, pcm16, n * sizeof(int16_t), 0);
  }
}
uint8_t frame[3 + 256 * sizeof(int16_t)];   // 1 byte type, 2 bytes length, then PCM

void setRecordingLeds(bool on) {
  digitalWrite(PIN_LED_REC, on);
  digitalWrite(PIN_LED_BOARD, on);
}

void loadConfig() {
  prefs.begin("mic", true);
  ssid = prefs.getString("ssid", "");
  pass = prefs.getString("pass", "");
  host = prefs.getString("host", "");
  token = prefs.getString("token", "");
  port = prefs.getUShort("port", 8800);
  prefs.end();
}

// Debounced edge of an active-low button: +1 pressed, -1 released, 0 nothing
int edge(int pin, bool &last, uint32_t &changed) {
  bool now = digitalRead(pin);
  if (now != last && millis() - changed > 30) {
    last = now;
    changed = millis();
    return now ? -1 : +1;
  }
  return 0;
}

// type 2 = record button pressed, 4 = released (hold to record), 3 = play the next unheard recording
void sendEvent(uint8_t type) {
  if (!client.connected()) return;
  uint8_t f[3] = {type, 0, 0};
  client.write(f, sizeof(f));
}

// Frame 9 = "I am alive": sent every 5 s during long blocking work (playing a long message, a firmware update),
// otherwise the server would take the silence for a lost board
void alive() {
  esp_task_wdt_reset();
  static uint32_t last = 0;
  if (millis() - last > 5000) { last = millis(); sendEvent(9); }
}

// Plain text for the server log (frame type 5)
void debugText(const char *t) {
  if (!client.connected()) return;
  uint8_t len = strlen(t);
  uint8_t h[3] = {5, len, 0};
  client.write(h, 3);
  client.write((const uint8_t *)t, len);
}

// Debug text for the server log (frame type 5)
void debugToServer(const char *fmt, uint32_t a = 0, uint32_t b = 0, uint32_t c = 0) {
  if (!client.connected()) return;
  char msg[100];
  int len = snprintf(msg, sizeof(msg), fmt, a, b, c);
  uint8_t h[3] = {5, (uint8_t)len, 0};
  client.write(h, 3);
  client.write((uint8_t *)msg, len);
}

uint32_t pressMs = 0, sentSincePress = 0;

void checkButtons() {
  static bool lastRec = true, lastPlay = true;
  static uint32_t chRec = 0, chPlay = 0;
  int r = edge(PIN_BTN_REC, lastRec, chRec);
  if (r > 0) { pressMs = millis(); sentSincePress = 0; sendEvent(2); }
  if (r < 0) {
    debugToServer("released: held %lu ms, audio sent %lu samples = %lu ms", millis() - pressMs, sentSincePress, sentSincePress / 16);
    sendEvent(4);
  }
  int p = edge(PIN_BTN_PLAY, lastPlay, chPlay);
  if (p > 0) { sendEvent(7); if (!streaming) sendEvent(3); }   // 7/8: pressed/released, for the button test on the web page
  if (p < 0) sendEvent(8);
}

// The board always listens and keeps the last 0.4 s, so the first word is not lost
// while the "start" command travels to the server and back.
const int PREROLL = 6400;
int16_t ring[PREROLL];
int ringPos = 0;
bool ringFull = false;
bool needPreroll = false;

void pushRing(const int16_t *s, int n) {
  for (int i = 0; i < n; i++) {
    ring[ringPos] = s[i];
    ringPos = (ringPos + 1) % PREROLL;
    if (ringPos == 0) ringFull = true;
  }
}

uint32_t statRead = 0, statSent = 0, statMaxWrite = 0, statLoops = 0, statT = 0;   // diagnostics while recording

void sendAudioFrame(int n) {              // n samples are already in frame+3
  uint16_t bytes = n * sizeof(int16_t);
  frame[0] = 1;
  frame[1] = bytes & 0xFF;
  frame[2] = bytes >> 8;
  size_t total = 3 + bytes, sent = 0;
  uint32_t t0 = micros();
  while (sent < total && client.connected()) sent += client.write(frame + sent, total - sent);
  uint32_t dt = micros() - t0;
  if (dt > statMaxWrite) statMaxWrite = dt;
  statSent += n;
  sentSincePress += n;
}

void sendPreroll() {                       // oldest sample first
  int count = ringFull ? PREROLL : ringPos;
  int start = ringFull ? ringPos : 0;
  int16_t *pcm = (int16_t *)(frame + 3);
  for (int i = 0; i < count; i += 256) {
    int n = min(256, count - i);
    for (int k = 0; k < n; k++) pcm[k] = ring[(start + i + k) % PREROLL];
    sendAudioFrame(n);
  }
}

// Setup over USB: lines SSID:..., PASS:..., HOST:..., PORT:..., then SAVE
void handleSerial() {
  static String line, nSsid, nPass, nHost, nToken;
  static uint16_t nPort = 8800;
  static bool hSsid = false, hPass = false, hHost = false, hPort = false, hToken = false;   // only what was sent is saved
  while (Serial.available()) {
    char c = Serial.read();
    if (c == '\r') continue;
    if (c != '\n') { line += c; continue; }
    if (line.startsWith("SSID:")) { nSsid = line.substring(5); hSsid = true; }
    else if (line.startsWith("PASS:")) { nPass = line.substring(5); hPass = true; }
    else if (line.startsWith("HOST:")) { nHost = line.substring(5); hHost = true; }
    else if (line.startsWith("PORT:")) { nPort = line.substring(5).toInt(); hPort = true; }
    else if (line.startsWith("TOKEN:")) { nToken = line.substring(6); hToken = true; }
    else if (line == "SAVE") {
      prefs.begin("mic", false);
      if (hSsid) prefs.putString("ssid", nSsid);
      if (hPass) prefs.putString("pass", nPass);
      if (hHost) prefs.putString("host", nHost);
      if (hPort) prefs.putUShort("port", nPort);
      if (hToken) prefs.putString("token", nToken);
      prefs.end();
      Serial.println("OK saved, rebooting");
      delay(300);
      ESP.restart();
    } else if (line == "STATUS") {
      Serial.printf("wifi=%s ip=%s host=%s:%u streaming=%d\n",
                    WiFi.status() == WL_CONNECTED ? "up" : "down", WiFi.localIP().toString().c_str(),
                    host.c_str(), port, streaming);
    }
    line = "";
  }
}

bool connectWifi() {
  if (ssid.length() == 0) {
    static uint32_t last = 0;
    if (millis() - last > 3000) { Serial.println("NO WIFI SETTINGS: run set_wifi.py"); last = millis(); }
    return false;
  }
  WiFi.disconnect(true, true);                      // drop any half-finished attempt, otherwise begin() fails
  delay(200);
  WiFi.mode(WIFI_STA);
  WiFi.setSleep(false);
  WiFi.begin(ssid.c_str(), pass.c_str());
  Serial.print("Wi-Fi connecting");
  for (int i = 0; i < 30 && WiFi.status() != WL_CONNECTED; i++) { delay(500); Serial.print("."); handleSerial(); esp_task_wdt_reset(); }
  Serial.println();
  if (WiFi.status() == WL_CONNECTED) {
    WiFi.setSleep(false);                           // no modem sleep: otherwise the router holds our packets for seconds
    esp_wifi_set_ps(WIFI_PS_NONE);
    Serial.printf("Wi-Fi up, IP %s, RSSI %d dBm\n", WiFi.localIP().toString().c_str(), WiFi.RSSI());
  }
  return WiFi.status() == WL_CONNECTED;
}

bool connectServer() {
  if (host.length() == 0) return false;
  if (!client.connect(host.c_str(), port, 3000)) { Serial.println("server not reachable"); return false; }
  client.setNoDelay(true);
  int on = 1, idle = 15, interval = 5, count = 3;     // TCP keepalive: a connection the router has forgotten is noticed in about 30 s
  setsockopt(client.fd(), SOL_SOCKET, SO_KEEPALIVE, &on, sizeof(on));
  setsockopt(client.fd(), IPPROTO_TCP, TCP_KEEPIDLE, &idle, sizeof(idle));
  setsockopt(client.fd(), IPPROTO_TCP, TCP_KEEPINTVL, &interval, sizeof(interval));
  setsockopt(client.fd(), IPPROTO_TCP, TCP_KEEPCNT, &count, sizeof(count));
  uint8_t hdr[42] = {'M', 'I', 'C', '2'};          // with a password the header is MIC3 + the password padded to 32 bytes
  uint32_t rate = RATE;
  uint16_t bits = 16;
  memcpy(hdr + 4, &rate, 4);
  memcpy(hdr + 8, &bits, 2);
  size_t hdrLen = 10;
  if (token.length() > 0) {
    hdr[3] = '3';
    memset(hdr + 10, 0, 32);
    memcpy(hdr + 10, token.c_str(), min((size_t)32, (size_t)token.length()));
    hdrLen = 42;
  }
  client.write(hdr, hdrLen);
  Serial.println("connected to server");
  debugText("firmware " __DATE__ " " __TIME__);
  return true;
}

// Playback is buffered: the network fills a 1.5 s buffer, a separate task feeds the speaker from it,
// so a Wi-Fi stall of a second or so does not turn into silence in the speaker.
StreamBufferHandle_t playBuf;
volatile bool playGate = false, playBusy = false;

void playTask(void *) {
  static int16_t chunk[256];
  for (;;) {
    if (!playGate && xStreamBufferBytesAvailable(playBuf) == 0) { vTaskDelay(pdMS_TO_TICKS(10)); continue; }
    if (!playGate) { vTaskDelay(pdMS_TO_TICKS(5)); continue; }          // still collecting the first second
    size_t got = xStreamBufferReceive(playBuf, chunk, sizeof(chunk), pdMS_TO_TICKS(20));
    if (got == 0) continue;
    playBusy = true;
    int n = got / 2;
    for (int i = 0; i < n; i++) chunk[i] = (int16_t)(chunk[i] * PLAY_VOLUME);
    i2sOut.write((uint8_t *)chunk, n * 2);
    playBusy = false;
  }
}

// The server sends a recording as PCM: buffer it while it arrives and play it
void playFromServer(uint32_t total) {
  static uint8_t tmp[1024];
  const size_t START_BYTES = 16000;                    // half a second is collected before the sound starts
  client.setTimeout(1000);
  digitalWrite(PIN_LED_BOARD, HIGH);
  xStreamBufferReset(playBuf);
  playGate = false;
  uint32_t left = total, idleSince = millis();
  while (left > 0 && client.connected()) {
    alive();
    size_t want = left < sizeof(tmp) ? left : sizeof(tmp);
    size_t got = client.readBytes(tmp, want);
    if (got == 0) {
      if (millis() - idleSince > 10000) break;          // the server went silent in the middle of a message
      continue;
    }
    idleSince = millis();
    got &= ~(size_t)1;                                   // whole 16-bit samples only
    size_t sent = 0;
    while (sent < got) {                                 // wait for room in the buffer, the speaker is playing it out
      sent += xStreamBufferSend(playBuf, tmp + sent, got - sent, pdMS_TO_TICKS(500));
      alive();
      if (!client.connected()) break;
    }
    left -= got;
    if (!playGate && xStreamBufferBytesAvailable(playBuf) >= START_BYTES) playGate = true;
  }
  playGate = true;                                       // short message: play what we have
  if (left > 0) {                                        // aborted: the rest of the PCM would be read as commands, start over
    Serial.println("playback aborted, reconnecting");
    xStreamBufferReset(playBuf);
    playGate = false;
    client.stop();
  } else {
    while ((xStreamBufferBytesAvailable(playBuf) > 0 || playBusy) && millis() - idleSince < 60000) { vTaskDelay(pdMS_TO_TICKS(10)); alive(); }
    playGate = false;
  }
  digitalWrite(PIN_LED_BOARD, LOW);
}

// ---- sounds: a sound is a list of notes {start Hz, end Hz, ms, loudness, decay ms (0 = none), 2nd harmonic} ----

void playNotes(const Note *notes, int count, float vol) {
  static int16_t buf[256];
  int fill = 0;
  float phase = 0;
  for (int k = 0; k < count; k++) {
    const Note &n = notes[k];
    int total = RATE * n.ms / 1000;
    for (int i = 0; i < total; i++) {
      float v = 0;
      if (n.f0 > 0) {
        phase += 2 * PI * (n.f0 + (n.f1 - n.f0) * i / total) / RATE;
        float env = min(1.0f, i / 80.0f) * min(1.0f, (total - i) / 80.0f);
        if (n.tau > 0) env *= expf(-(i * 1000.0f / RATE) / n.tau);
        v = n.amp * env * (sinf(phase) + n.h2 * sinf(2 * phase)) / (1 + n.h2);
      }
      buf[fill++] = (int16_t)(14000 * vol * PLAY_VOLUME * v);
      if (fill == 256) { i2sOut.write((uint8_t *)buf, sizeof(buf)); fill = 0; }
    }
  }
  if (fill) i2sOut.write((uint8_t *)buf, fill * 2);
}

#define SND(a) playNotes(a, sizeof(a) / sizeof(a[0]), vol)
// New-message sounds 1..10, start-up sounds 102..104 (101 is the PlayStation one, bootSound below)
void bootSound();
void playSound(int id, float vol) {
  static const Note drop[]   = {{600, 1500, 170, 1.0f, 60, 0}, {0, 0, 50, 0, 0, 0}, {900, 900, 220, 0.3f, 70, 0}};
  static const Note uhoh[]   = {{880, 820, 130, 1.0f, 0, 0.35f}, {0, 0, 30, 0, 0, 0}, {620, 520, 200, 1.0f, 90, 0.35f}};
  static const Note bell[]   = {{1568, 1568, 900, 1.0f, 300, 0.4f}};
  static const Note up3[]    = {{523, 523, 140, 1.0f, 90, 0.2f}, {659, 659, 140, 1.0f, 90, 0.2f}, {784, 784, 320, 1.0f, 150, 0.2f}};
  static const Note dingdong[] = {{880, 880, 380, 1.0f, 200, 0.25f}, {660, 660, 600, 1.0f, 300, 0.25f}};
  static const Note ping[]   = {{1200, 1200, 320, 0.9f, 80, 0}};
  static const Note sonar[]  = {{1000, 1000, 500, 1.0f, 220, 0}, {0, 0, 250, 0, 0, 0}, {1000, 1000, 500, 0.4f, 220, 0}};
  static const Note radio[]  = {{1700, 1700, 80, 1.0f, 0, 0.3f}, {0, 0, 70, 0, 0, 0}, {1700, 1700, 80, 1.0f, 0, 0.3f}};
  static const Note soft[]   = {{440, 440, 260, 0.8f, 140, 0.1f}, {554, 554, 420, 0.8f, 220, 0.1f}};
  static const Note arcade[] = {{523, 523, 80, 1.0f, 0, 0.3f}, {659, 659, 80, 1.0f, 0, 0.3f}, {784, 784, 80, 1.0f, 0, 0.3f}, {1047, 1047, 260, 1.0f, 120, 0.3f}};
  static const Note bootSoft[] = {{392, 392, 180, 0.9f, 120, 0.2f}, {523, 523, 180, 0.9f, 120, 0.2f}, {659, 659, 420, 0.9f, 220, 0.2f}};
  static const Note bootBeep[] = {{1000, 1000, 130, 0.9f, 0, 0}};
  static const Note bootTwo[]  = {{659, 659, 160, 0.9f, 100, 0.2f}, {988, 988, 450, 0.9f, 240, 0.2f}};
  switch (id) {
    case 1: SND(drop); break;
    case 2: SND(uhoh); break;
    case 3: SND(bell); break;
    case 4: SND(up3); break;
    case 5: SND(dingdong); break;
    case 6: SND(ping); break;
    case 7: SND(sonar); break;
    case 8: SND(radio); break;
    case 9: SND(soft); break;
    case 10: SND(arcade); break;
    case 101: bootSound(); break;
    case 102: SND(bootSoft); break;
    case 103: SND(bootBeep); break;
    case 104: SND(bootTwo); break;
  }
}

// Power-on sound in the spirit of the PlayStation start-up: a deep pad swells, then a bright shimmering chord rings out
void bootSound() {
  const float pad[3] = {82.4f, 123.5f, 164.8f};                    // E2 B2 E3
  const float bell[4] = {659.3f, 987.8f, 1318.5f, 1661.2f};        // E5 B5 E6 G#6
  const float total = 2.6f, bellAt = 0.9f;
  const int n = RATE * total;
  static int16_t buf[256];
  size_t wrote = 0;
  int peak = 0;
  float pp[3] = {0, 0, 0}, pb[4] = {0, 0, 0, 0}, pd[4] = {0, 0, 0, 0};
  for (int i = 0; i < n; i += 256) {
    int m = min(256, n - i);
    for (int k = 0; k < m; k++) {
      float t = (i + k) / (float)RATE;
      float v = 0;
      float padEnv = (t < 0.7f ? t / 0.7f : 1.0f) * expf(-max(0.0f, t - 0.9f) / 0.9f);
      for (int j = 0; j < 3; j++) { pp[j] += 2 * PI * pad[j] / RATE; v += 0.45f * padEnv * sinf(pp[j]); }
      if (t > bellAt) {
        float u = t - bellAt, env = min(1.0f, u / 0.01f) * expf(-u / 0.7f);
        for (int j = 0; j < 4; j++) {                                // each bell is two slightly detuned sines: the shimmer
          pb[j] += 2 * PI * bell[j] / RATE;
          pd[j] += 2 * PI * (bell[j] * 1.004f) / RATE;
          v += 0.16f * env * (sinf(pb[j]) + sinf(pd[j])) * (1.0f - 0.12f * j);
        }
      }
      float fade = (n - (i + k) < 800) ? (n - (i + k)) / 800.0f : 1.0f;
      buf[k] = (int16_t)(11000 * PLAY_VOLUME * fade * v);
      if (abs(buf[k]) > peak) peak = abs(buf[k]);
    }
    wrote += i2sOut.write((uint8_t *)buf, m * 2);
  }
  Serial.printf("boot sound: wrote %u of %u bytes, peak %d\n", (unsigned)wrote, (unsigned)(n * 2), peak);
}

// Over-the-air update: the server sends 'O', the size (4 bytes), the MD5 as 32 hex characters and the firmware image.
// The image goes to the other flash slot and is checked against the MD5; only then the board switches to it and restarts.
void drainBytes(uint32_t n) {
  static uint8_t trash[512];
  uint32_t idle = millis();
  while (n > 0 && client.connected() && millis() - idle < 15000) {
    alive();
    size_t got = client.readBytes(trash, n < sizeof(trash) ? n : sizeof(trash));
    if (got) { n -= got; idle = millis(); }
  }
}

void otaFromServer() {
  uint8_t h[36];
  client.setTimeout(3000);
  if (client.readBytes(h, 36) != 36) return;
  uint32_t size = h[0] | (h[1] << 8) | (h[2] << 16) | ((uint32_t)h[3] << 24);
  char md5[33];
  memcpy(md5, h + 4, 32);
  md5[32] = 0;
  streaming = false;
  debugToServer("ota: receiving %lu bytes", size);
  if (size == 0 || !Update.begin(size)) {
    debugToServer("ota: no room for the image, skipped");
    drainBytes(size);
    return;
  }
  Update.setMD5(md5);
  static uint8_t buf[1024];
  uint32_t left = size, idle = millis(), lastTenth = 0;
  client.setTimeout(2000);
  while (left > 0 && client.connected()) {
    alive();
    size_t got = client.readBytes(buf, left < sizeof(buf) ? left : sizeof(buf));
    if (got == 0) {
      if (millis() - idle > 15000) break;
      continue;
    }
    idle = millis();
    if (Update.write(buf, got) != got) break;
    left -= got;
    uint32_t tenth = (size - left) / (size / 10 + 1);
    if (tenth != lastTenth) { lastTenth = tenth; debugToServer("ota: %lu0 %%", tenth); }
  }
  if (left == 0 && Update.end(true)) {
    debugToServer("ota: done, restarting");
    delay(500);
    ESP.restart();
  }
  Serial.print("ota failed: ");
  Serial.println(Update.errorString());
  Update.abort();
  client.stop();                                   // the rest of the image must not be read as commands: start over
}

// From the server: 'R' start, 'S' stop, 'U'+count (unheard recordings), 'N' nothing to play, 'L'+id+state set an LED, 'O' firmware update, 'B'+id+vol new-message sound, 'K'+id+vol sound without acknowledgement, 'W'+n start-up sound choice, 'P'+len+PCM play
void handleIncoming() {
  while (client.available()) {
    int c = client.read();
    if (c == 'R') {
      streaming = true; needPreroll = true; setRecordingLeds(true);
      debugToServer("start command arrived %lu ms after the press", millis() - pressMs);
    }
    else if (c == 'S') { streaming = false; setRecordingLeds(false); }
    else if (c == 'U') { uint8_t n = 0; if (client.readBytes(&n, 1) == 1) digitalWrite(PIN_LED_NEW, n > 0); }
    else if (c == 'B') { uint8_t b[2]; if (client.readBytes(b, 2) == 2) { if (!streaming) playSound(b[0], b[1] / 100.0f); sendEvent(6); } }   // new message: sound id, loudness %, acknowledged
    else if (c == 'K') { uint8_t b[2]; if (client.readBytes(b, 2) == 2 && !streaming) playSound(b[0], b[1] / 100.0f); }                      // preview or repeat: no acknowledgement
    else if (c == 'W') { uint8_t b = 0; if (client.readBytes(&b, 1) == 1) { prefs.begin("mic", false); if (prefs.getUChar("boot", 1) != b) prefs.putUChar("boot", b); prefs.end(); } }   // start-up sound choice: 0 = none, 1..4
    else if (c == 'L') { uint8_t b[2]; if (client.readBytes(b, 2) == 2 && b[0] < 3) { const int pins[3] = {PIN_LED_NEW, PIN_LED_REC, PIN_LED_BOARD}; digitalWrite(pins[b[0]], b[1]); } }   // LED test from the web page
    else if (c == 'O') otaFromServer();
    else if (c == 'N') Serial.println("nothing new to play");
    else if (c == 'P') {
      uint8_t b[4];
      if (client.readBytes(b, 4) == 4) playFromServer(b[0] | (b[1] << 8) | (b[2] << 16) | ((uint32_t)b[3] << 24));
    }
  }
}

void setup() {
  Serial.begin(115200);
  setCpuFrequencyMhz(160);                          // enough for 16 kHz audio + Wi-Fi, noticeably less power than 240 MHz
  pinMode(PIN_BTN_REC, INPUT_PULLUP);
  pinMode(PIN_BTN_PLAY, INPUT_PULLUP);
  pinMode(PIN_LED_REC, OUTPUT);
  pinMode(PIN_LED_NEW, OUTPUT);
  pinMode(PIN_LED_BOARD, OUTPUT);
  setRecordingLeds(false);
  i2s.setPins(PIN_BCLK, PIN_WS, -1, PIN_DIN);
  // INMP441: 24 bits left-justified in a 32-bit slot, L/R tied to GND = left channel
  if (!i2s.begin(I2S_MODE_STD, RATE, I2S_DATA_BIT_WIDTH_32BIT, I2S_SLOT_MODE_MONO, I2S_STD_SLOT_LEFT)) {
    Serial.println("I2S (mic) init failed");
    while (true) delay(1000);
  }
  i2sOut.setPins(PIN_OUT_BCLK, PIN_OUT_LRC, PIN_OUT_DIN);
  if (!i2sOut.begin(I2S_MODE_STD, RATE, I2S_DATA_BIT_WIDTH_16BIT, I2S_SLOT_MODE_MONO, I2S_STD_SLOT_BOTH))
    Serial.println("I2S (speaker) init failed");
  // the audio tasks start only when both I2S channels are up (the order the diagnostic found working)
  micBuf = xStreamBufferCreate(64 * 1024, 2);
  xTaskCreatePinnedToCore(micTask, "mic", 4096, NULL, 5, NULL, 1);
  playBuf = xStreamBufferCreate(48 * 1024, 2);
  xTaskCreatePinnedToCore(playTask, "play", 4096, NULL, 5, NULL, 1);
  esp_reset_reason_t why = esp_reset_reason();      // sound only when power is applied or the board is reset by hand, not after a crash or watchdog reboot
  if (why == ESP_RST_POWERON || why == ESP_RST_EXT) {
    prefs.begin("mic", true);
    int choice = prefs.getUChar("boot", 1);
    prefs.end();
    if (choice >= 1 && choice <= 4) playSound(100 + choice, 1.0f);
  }
  loadConfig();
  esp_task_wdt_config_t wdt = {.timeout_ms = 30000, .idle_core_mask = 0, .trigger_panic = true};   // a hung loop reboots the board
  if (esp_task_wdt_reconfigure(&wdt) != ESP_OK) esp_task_wdt_init(&wdt);
  esp_task_wdt_add(NULL);
  WiFi.setAutoReconnect(true);
  // why a Wi-Fi attempt fails: 15 or 202 = wrong password, 201 = network not found
  WiFi.onEvent([](arduino_event_id_t, arduino_event_info_t info) {
    Serial.printf("\nWi-Fi disconnected, reason %d\n", info.wifi_sta_disconnected.reason);
  }, ARDUINO_EVENT_WIFI_STA_DISCONNECTED);
  Serial.printf("mic_stream ready, network '%s'\n", ssid.c_str());
}

void loop() {
  esp_task_wdt_reset();
  if (streaming) digitalWrite(PIN_LED_REC, (millis() / 250) % 2 == 0);   // blinks twice a second while recording
  static uint32_t lastOk = 0;                         // no server for 5 minutes (Wi-Fi gone for good, router rebooted): restart the board
  if (client.connected()) lastOk = millis();
  else if (ssid.length() > 0 && millis() - lastOk > 300000) { Serial.println("no server for 5 min, restarting"); delay(100); ESP.restart(); }
  static uint32_t lastIter = 0;
  uint32_t gap = millis() - lastIter;
  lastIter = millis();
  if (lastIter > 5000 && gap > 300) debugToServer("main loop was stuck for %lu ms (RSSI -%lu dBm)", gap, (uint32_t)(-WiFi.RSSI()));
  handleSerial();
  static uint32_t lastRssi = 0;                       // signal strength into the server log: helps to find a good place for the board
  if (client.connected() && millis() - lastRssi > 15000) {
    lastRssi = millis();
    debugToServer("Wi-Fi signal -%lu dBm (good: up to -65, poor: below -80)", (uint32_t)(-WiFi.RSSI()));
  }
  if (WiFi.status() != WL_CONNECTED) {
    streaming = false;
    setRecordingLeds(false);
    if (!connectWifi()) { delay(200); return; }
  }
  if (!client.connected()) {
    streaming = false;
    setRecordingLeds(false);
    digitalWrite(PIN_LED_NEW, LOW);
    if (!connectServer()) { delay(2000); return; }
  }
  checkButtons();
  handleIncoming();
  // always listen (the last 0.4 s is kept), send only while the button is held
  int16_t *pcm = (int16_t *)(frame + 3);
  size_t bytes = xStreamBufferReceive(micBuf, pcm, 256 * sizeof(int16_t), pdMS_TO_TICKS(50));
  int n = bytes / sizeof(int16_t);
  if (n == 0) return;
  pushRing(pcm, n);
  statRead += n;
  statLoops++;
  if (streaming && millis() - statT > 2000) {
    Serial.printf("stats 2s: read %u samples, sent %u, loops %u, slowest write %u us, heap %u\n",
                  statRead, statSent, statLoops, statMaxWrite, ESP.getFreeHeap());
    statRead = statSent = statMaxWrite = statLoops = 0;
    statT = millis();
  }
  if (!streaming) return;
  if (needPreroll) { sendPreroll(); needPreroll = false; }   // the ring already holds the current block
  else sendAudioFrame(n);
}

// OTA self-test build
