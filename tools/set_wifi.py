#!/usr/bin/env python3
"""Store the Wi-Fi settings and the PC address in the ESP32 (once, over USB).
Run it yourself in a terminal: the password is typed here and goes straight to the board, nowhere else.

    python set_wifi.py            (the board must be connected by USB, nothing else may use the port)
"""
import getpass, socket, sys, time

import serial
from serial.tools import list_ports


def find_port():
    for p in list_ports.comports():
        if any(k in (p.description or '') for k in ('CH340', 'CP210', 'USB-SERIAL', 'USB Serial')):
            return p.device
    return None


def local_ip():
    s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    try:
        s.connect(('8.8.8.8', 80))
        return s.getsockname()[0]
    except OSError:
        return ''
    finally:
        s.close()


args = [a for a in sys.argv[1:] if not a.startswith('--')]
show = '--show' in sys.argv                      # --show: the password is visible while typing (to catch typos)
port = args[0] if args else find_port()
if not port:
    sys.exit('Плата не найдена. Укажите порт: python set_wifi.py COM11')
print('Плата на', port)
ssid = input('Имя Wi-Fi (только 2,4 ГГц): ').strip()
if show:
    password = input('Пароль Wi-Fi (виден при вводе): ')
else:
    password = getpass.getpass('Пароль Wi-Fi (ввод не виден): ')
print(f'Имя сети: "{ssid}", длина пароля: {len(password)} симв.')
host = input(f'Адрес этого ПК [{local_ip()}]: ').strip() or local_ip()
tcp = input('Порт сервера [8800]: ').strip() or '8800'

s = serial.Serial(port, 115200, timeout=1)
s.dtr = s.rts = False
time.sleep(0.5)
for line in (f'SSID:{ssid}', f'PASS:{password}', f'HOST:{host}', f'PORT:{tcp}', 'SAVE'):
    s.write((line + '\n').encode('utf-8'))
    time.sleep(0.1)
t0 = time.time()
out = b''
while time.time() - t0 < 4:
    out += s.read(256)
    if b'saved' in out:
        break
print('Готово, плата перезагружается.' if b'saved' in out else 'Ответа нет: проверьте, что на плате прошивка mic_stream.')
