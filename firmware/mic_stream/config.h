// Pins and constants of the microphone board. Wi-Fi and the PC address are NOT here: tools/set_wifi.py writes them to the board.
#pragma once

const int PIN_BCLK = 26, PIN_WS = 25, PIN_DIN = 33;          // microphone
const int PIN_OUT_BCLK = 21, PIN_OUT_LRC = 19, PIN_OUT_DIN = 22;   // speaker amplifier
const int PIN_BTN_REC = 27, PIN_BTN_PLAY = 13;
const int PIN_LED_REC = 4, PIN_LED_NEW = 18, PIN_LED_BOARD = 2;
const int PIN_AMP_SD = 23;                                   // MAX98357A SD: LOW = amplifier off (no hiss in silence); unconnected SD keeps it always on
const int PIN_BATT = 34;                                     // 18650 through a 100k/100k divider; ADC1 only, ADC2 stops working with Wi-Fi on
const float BATT_DIVIDER = 2.0f;
const int RATE = 16000;
const float MIC_HIGHPASS_HZ = 120;                           // cuts the microphone's DC offset and hum before ADPCM
const float PLAY_VOLUME = 1.0f;                              // software volume of the speaker, 0..1
