# ESP32 + INMP441: микрофон, колонка и Telegram

[English](README.md) · **Русский**

Плата ESP32 слушает микрофон INMP441. Пока удержана кнопка, звук по Wi-Fi идёт на сервер на ПК: он пишет WAV, ведёт очередь
«не прослушано» и пересылает записи в Telegram. Голосовые из Telegram возвращаются в очередь, и плата проигрывает их на колонке.

```
 ESP32 (firmware/) ──Wi-Fi, TCP 8800──▶ сервер (server/) ──▶ recordings/*.wav
   микрофон, кнопки, колонка                  ├──▶ Telegram-бот
                                              └── страница http://127.0.0.1:8801
```

## Структура

```
esp32-voice-box/
├─ firmware/mic_stream/    прошивка ESP32: mic_stream.ino + config.h (пины и константы)
├─ server/                 сервер на Go: main, board, queue, wav, web (+web/index.html), telegram, dotenv
├─ legacy/server-py/       первая версия сервера на Python (запасная)
├─ tools/set_wifi.py       записать Wi-Fi и адрес ПК в плату по USB
├─ scripts/                build-server · run-server · flash-firmware · test (PowerShell)
├─ tests/run_tg_test.py    проверка без железа: подставная плата и Telegram
├─ config/                 примеры настроек
├─ docs/                   подключение, протокол, Telegram, если не работает
├─ .env.example            образец .env (сам .env в .gitignore)
└─ recordings/             записи (создаётся при запуске, в .gitignore)
```

## Быстрый старт

1. **Прошивка:** `.\scripts\flash-firmware.ps1 -Port COM11`
2. **Wi-Fi** (сеть 2,4 ГГц): при первом включении коробка открывает сеть `VoiceBox-XXXX`. Подключитесь к ней с телефона,
   страница настройки откроется сама. Чтобы сменить сеть позже, держите обе кнопки 3 секунды при включении.
   По USB тоже можно: `python tools\set_wifi.py COM11 --show`
3. **Сервер:** `.\scripts\run-server.ps1`, затем откройте http://127.0.0.1:8801
4. Подключите микрофон, кнопки и колонку по [docs/wiring.md](docs/wiring.md). Зажали кнопку записи: говорите, отпустили: запись сохранена.

Windows при первом запуске спросит разрешение для сети: разрешите для частных сетей, иначе плата не достучится до порта 8800.

## Настройки

Секреты лежат в `.env` в корне проекта (образец: `.env.example`), в репозиторий он не попадает:

| Переменная | Что |
|---|---|
| `TELEGRAM_BOT_TOKEN` (допустимы `TELEGRAM_BOT`, `TELEGRAM_TOKEN`) | токен бота от @BotFather |
| `TELEGRAM_CHAT_ID` | числовой ID чата; пока пуст, бот в режиме привязки |
| `TELEGRAM_THREAD_ID` | номер топика группы-форума (команда `/id` в топике), пусто если топиков нет |
| `MIC_REC_DIR`, `MIC_GAIN`, `MIC_MAX_SECONDS`, `FFMPEG` | папка записей, усиление (4), предел записи (300 с), путь к ffmpeg |
| `MIC_REMIND_SECONDS` | как часто напоминать звуком о непрослушанных (5 с, 0 = выключено) |
| `MIC_TAIL_MS` | сколько ещё писать после отпускания кнопки (2500 мс; тишина в конце обрезается) |
| `AZURE_SPEECH_KEY`, `AZURE_SPEECH_REGION`, `AZURE_SPEECH_VOICE` | озвучка текстовых сообщений голосом Microsoft (без них текст не принимается) |
| `TTS_PITCH`, `TTS_RATE`, `TTS_EFFECT` | мультяшный голос: высота и темп (Azure), эффект `chipmunk` или `robot` |
| `EDGE_TTS` | путь к edge-tts: голоса Microsoft без ключа, неофициально, для домашнего использования |
| `PIPER`, `PIPER_MODEL` | озвучка без облака через Piper, если не заданы Azure и edge-tts |

Подробнее: [Telegram](docs/telegram.md) · [протокол](docs/protocol.md) · [если не работает](docs/troubleshooting.md).

## Проверка

`.\scripts\test.ps1` собирает сервер и гоняет 13 проверок с подставной платой и подставным Telegram (свои порты и папка `tests\run\`,
реальные записи и токен не затрагиваются).

## Лицензия

[MIT](LICENSE). Сторонний код: [THIRD_PARTY.md](THIRD_PARTY.md). Как помочь проекту: [CONTRIBUTING.md](CONTRIBUTING.md).
