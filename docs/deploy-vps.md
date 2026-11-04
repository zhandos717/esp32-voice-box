# Сервер на VPS (компьютер можно выключать)

Схема: плата (Wi-Fi дома) → интернет → `box.zhandos.top:8800` (аудио-порт сервера на VPS, с паролем).
Страница управления (порт 8801) слушает только `127.0.0.1` и наружу выходит через nginx с логином (уже настроено).

## 1. Обновить сервер на VPS

```bash
cd ~/esp32-voice-box && git pull
cd server && go build -o mic_server .        # нужен Go; либо соберите на ПК: GOOS=linux GOARCH=amd64 go build
sudo apt install -y ffmpeg                    # нужен для голосовых из Telegram (ogg/opus)
```

## 2. Настройки (`.env` рядом с сервером или переменные сервиса)

```
TELEGRAM_BOT=...            # токен бота
TELEGRAM_CHAT_ID=...        # номер чата
TELEGRAM_THREAD_ID=...      # номер топика (если есть)
MIC_TOKEN=придумайте-пароль # пароль платы, до 32 символов, без пробелов
MIC_REC_DIR=/var/lib/mic/recordings
```

Пароль `MIC_TOKEN` защищает аудио-порт: без него любой в интернете мог бы подключиться «как плата».

## 3. Автозапуск (systemd)

`/etc/systemd/system/mic-server.service`:

```
[Unit]
Description=ESP32 voice box server
After=network-online.target

[Service]
WorkingDirectory=/home/USER/esp32-voice-box
EnvironmentFile=/home/USER/esp32-voice-box/.env
ExecStart=/home/USER/esp32-voice-box/server/mic_server
Restart=always
RestartSec=3
User=USER

[Install]
WantedBy=multi-user.target
```

```bash
sudo mkdir -p /var/lib/mic/recordings && sudo chown USER /var/lib/mic/recordings
sudo systemctl daemon-reload && sudo systemctl enable --now mic-server
sudo ufw allow 8800/tcp          # аудио-порт для платы (если включён ufw)
journalctl -u mic-server -f      # лог
```

## 4. Переключение (порядок важен: у бота должен быть один сервер)

1. На домашнем ПК остановить сервер (`Stop-Process mic_server`), иначе Telegram выдаст `Conflict`.
2. На VPS запустить сервис (шаг 3).
3. Плата по USB к ПК, один раз:
   ```
   python tools/set_wifi.py --server
   ```
   адрес `box.zhandos.top`, порт `8800`, пароль = `MIC_TOKEN`. Wi-Fi плата помнит, его вводить не нужно.
4. Проверка: в логе VPS `ESP32 connected`, в Telegram команда `/status`.

Домашний сервер остаётся запасным: чтобы вернуться, `set_wifi.py --server` с адресом ПК и пустым паролем.

## Обновление прошивки с VPS

Соберите прошивку на ПК (`scripts/ota.ps1` соберёт её в `firmware/build/mic_stream.ino.bin`), скопируйте файл на VPS
в `firmware/build/` и на VPS выполните:

```bash
curl -X POST http://127.0.0.1:8801/api/ota
```

## Безопасность

- Аудио идёт по обычному TCP (без шифрования): пароль защищает от посторонних подключений, но не от того, кто «слушает» канал по пути.
- Не открывайте порт 8801 наружу без логина: через него можно прошить плату.
