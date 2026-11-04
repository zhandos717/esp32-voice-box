// Diagnostic: finds out which way of starting the microphone and the speaker together gives sound.
// N short ticks are played through the known-good setup, then a 0.6 s beep follows: N = the variant that is tested.
//   1  speaker alone (the known-good case)
//   2  mic on port 0 + speaker on port 1, mic idle
//   3  the same, the mic is read all the time (like in the main firmware)
//   4  speaker on port 0 + mic on port 1, mic is read all the time
//   5  both with automatic port choice, mic is read all the time
#include <ESP_I2S.h>
#include "../mic_stream/config.h"

I2SClass *mic = nullptr, *spk = nullptr;
volatile bool reading = false;
int32_t rawbuf[256];

void readerTask(void *) {
  while (reading) mic->readBytes((char *)rawbuf, sizeof(rawbuf));
  vTaskDelete(NULL);
}

bool startSpk(int port) {
  spk = new I2SClass((i2s_port_t)port);
  spk->setPins(PIN_OUT_BCLK, PIN_OUT_LRC, PIN_OUT_DIN);
  return spk->begin(I2S_MODE_STD, RATE, I2S_DATA_BIT_WIDTH_16BIT, I2S_SLOT_MODE_MONO, I2S_STD_SLOT_BOTH);
}

bool startMic(int port) {
  mic = new I2SClass((i2s_port_t)port);
  mic->setPins(PIN_BCLK, PIN_WS, -1, PIN_DIN);
  return mic->begin(I2S_MODE_STD, RATE, I2S_DATA_BIT_WIDTH_32BIT, I2S_SLOT_MODE_MONO, I2S_STD_SLOT_LEFT);
}

void startReading() {
  reading = true;
  xTaskCreatePinnedToCore(readerTask, "rd", 4096, NULL, 5, NULL, 1);
  delay(200);
}

void stopAll() {
  reading = false;
  delay(200);
  if (spk) { spk->end(); delete spk; spk = nullptr; }
  if (mic) { mic->end(); delete mic; mic = nullptr; }
  delay(500);
}

void beep() {
  static int16_t b[256];
  const int n = RATE * 600 / 1000;
  for (int i = 0; i < n; i += 256) {
    int m = min(256, n - i);
    for (int k = 0; k < m; k++) b[k] = (int16_t)(9000 * sinf(2 * PI * 600.0f * (i + k) / RATE));
    spk->write((uint8_t *)b, m * 2);
  }
}

void announce(int n) {            // N short ticks through the known-good speaker setup, then the variant is tested
  Serial.printf("variant %d\n", n);
  delay(1500);
  startSpk(I2S_NUM_AUTO);
  static int16_t b[256];
  for (int t = 0; t < n; t++) {
    for (int i = 0; i < 1600; i += 256) {
      int m = min(256, 1600 - i);
      for (int k = 0; k < m; k++) b[k] = (int16_t)(9000 * sinf(2 * PI * 1500.0f * (i + k) / RATE));
      spk->write((uint8_t *)b, m * 2);
    }
    memset(b, 0, sizeof(b));
    for (int i = 0; i < 4800; i += 256) spk->write((uint8_t *)b, 512);
  }
  stopAll();
  delay(700);
}

void setup() {
  Serial.begin(115200);
  pinMode(PIN_LED_BOARD, OUTPUT);
  delay(1500);
}

void loop() {
  announce(1); Serial.println(startSpk(I2S_NUM_AUTO)); beep(); stopAll(); delay(1500);
  announce(2); Serial.println(startMic(0)); Serial.println(startSpk(1)); beep(); stopAll(); delay(1500);
  announce(3); Serial.println(startMic(0)); Serial.println(startSpk(1)); startReading(); beep(); stopAll(); delay(1500);
  announce(4); Serial.println(startSpk(0)); Serial.println(startMic(1)); startReading(); beep(); stopAll(); delay(1500);
  announce(5); Serial.println(startMic(I2S_NUM_AUTO)); Serial.println(startSpk(I2S_NUM_AUTO)); startReading(); beep(); stopAll(); delay(4000);
}
