// Speaker test: plays a rising three-tone beep every 3 seconds and on every press of the "next" button.
// No Wi-Fi, no server. Pins are shared with the main firmware (mic_stream/config.h).
#include <ESP_I2S.h>
#include "../mic_stream/config.h"

I2SClass i2sOut;

void tone(float hz, int ms) {
  const int n = RATE * ms / 1000;
  static int16_t buf[256];
  int i = 0;
  while (i < n) {
    int m = min(256, n - i);
    for (int k = 0; k < m; k++) {
      float env = 1.0f;
      int pos = i + k;
      if (pos < 200) env = pos / 200.0f;                 // fade in/out to avoid clicks
      if (n - pos < 200) env = (n - pos) / 200.0f;
      buf[k] = (int16_t)(12000 * PLAY_VOLUME * env * sinf(2 * PI * hz * pos / RATE));
    }
    i2sOut.write((uint8_t *)buf, m * 2);
    i += m;
  }
}

void beep() {
  digitalWrite(PIN_LED_BOARD, HIGH);
  tone(440, 250); tone(660, 250); tone(880, 400);
  digitalWrite(PIN_LED_BOARD, LOW);
  Serial.println("beep played");
}

void setup() {
  Serial.begin(115200);
  pinMode(PIN_LED_BOARD, OUTPUT);
  pinMode(PIN_BTN_PLAY, INPUT_PULLUP);
  i2sOut.setPins(PIN_OUT_BCLK, PIN_OUT_LRC, PIN_OUT_DIN);
  if (!i2sOut.begin(I2S_MODE_STD, RATE, I2S_DATA_BIT_WIDTH_16BIT, I2S_SLOT_MODE_MONO, I2S_STD_SLOT_BOTH))
    Serial.println("speaker I2S failed");
  else
    Serial.println("speaker test: BCLK21 LRC19 DIN22");
}

void loop() {
  static uint32_t last = 0;
  if (millis() - last > 3000 || digitalRead(PIN_BTN_PLAY) == LOW) {
    beep();
    last = millis();
    while (digitalRead(PIN_BTN_PLAY) == LOW) delay(10);
  }
}
