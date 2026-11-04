"""Telegram two-way bridge for mic_server.

  Telegram voice message -> converted to 16 kHz mono PCM -> saved as tg_*.wav in the recordings folder
                            (it joins the queue of unheard recordings, the board plays it on the next button press)
  Recording from the board -> sent to the chat as a voice message (or as a WAV file if ffmpeg is missing)

Settings are read from telegram.json next to this file (create it yourself, the token never goes anywhere else):
  {"token": "<bot token from @BotFather>", "chat_id": <your numeric chat id, or null while pairing>}

Pairing: while chat_id is null the bot answers any message with that chat's numeric id and ignores everything else.
Write the id into telegram.json and restart the server: from then on only that chat is served.
ffmpeg is needed for OGG/Opus (Telegram voice): set FFMPEG, or put ffmpeg(.exe) in ./ffmpeg/ or on PATH.
"""
import io, json, os, shutil, subprocess, threading, time, urllib.error, urllib.request, uuid, wave
from datetime import datetime

HERE = os.path.dirname(os.path.abspath(__file__))
CONFIG = os.environ.get('TELEGRAM_CONFIG') or os.path.join(HERE, 'telegram.json')
API_BASE = os.environ.get('TELEGRAM_API_BASE', 'https://api.telegram.org').rstrip('/')
RATE = 16000
MAX_VOICE_BYTES = 8 * 1024 * 1024


def find_ffmpeg():
    cand = [os.environ.get('FFMPEG'), os.path.join(HERE, 'ffmpeg', 'ffmpeg.exe'), os.path.join(HERE, 'ffmpeg', 'ffmpeg'),
            shutil.which('ffmpeg')]
    for c in cand:
        if c and os.path.isfile(c):
            return c
    return None


def decode_to_pcm(data):
    """Any audio Telegram can send -> raw 16 kHz mono int16. WAV is read directly, the rest goes through ffmpeg."""
    if data[:4] == b'RIFF':
        with wave.open(io.BytesIO(data), 'rb') as w:
            if w.getnchannels() == 1 and w.getsampwidth() == 2 and w.getframerate() == RATE:
                return w.readframes(w.getnframes())
    ff = find_ffmpeg()
    if not ff:
        raise RuntimeError('нужен ffmpeg для голосовых Telegram (положите его в папку ffmpeg рядом с сервером)')
    r = subprocess.run([ff, '-v', 'error', '-i', 'pipe:0', '-ac', '1', '-ar', str(RATE), '-f', 's16le', 'pipe:1'],
                       input=data, capture_output=True, timeout=60)
    if r.returncode != 0 or not r.stdout:
        raise RuntimeError('ffmpeg не смог прочитать аудио')
    return r.stdout


def wav_to_ogg_opus(wav_path):
    ff = find_ffmpeg()
    if not ff:
        return None
    r = subprocess.run([ff, '-v', 'error', '-i', wav_path, '-c:a', 'libopus', '-b:a', '24k', '-f', 'ogg', 'pipe:1'],
                       capture_output=True, timeout=120)
    return r.stdout if r.returncode == 0 and r.stdout else None


def _multipart(fields, files):
    boundary = uuid.uuid4().hex
    parts = []
    for k, v in fields.items():
        parts.append(f'--{boundary}\r\nContent-Disposition: form-data; name="{k}"\r\n\r\n{v}\r\n'.encode())
    for k, (fname, data, ctype) in files.items():
        parts.append(f'--{boundary}\r\nContent-Disposition: form-data; name="{k}"; filename="{fname}"\r\n'
                     f'Content-Type: {ctype}\r\n\r\n'.encode() + data + b'\r\n')
    parts.append(f'--{boundary}--\r\n'.encode())
    return b''.join(parts), f'multipart/form-data; boundary={boundary}'


class Bridge:
    def __init__(self, token, chat_id, rec_dir, on_new, log):
        self.token, self.chat_id, self.rec_dir, self.on_new, self.log = token, chat_id, rec_dir, on_new, log
        self.offset_file = os.path.join(rec_dir, '.tg_offset')
        self.stop = False

    # --- Bot API (errors never contain the token: it is part of the URL) ---
    def api(self, method, params=None, files=None, timeout=35):
        url = f'{API_BASE}/bot{self.token}/{method}'
        if files:
            body, ctype = _multipart({k: str(v) for k, v in (params or {}).items()}, files)
        else:
            body, ctype = json.dumps(params or {}).encode(), 'application/json'
        req = urllib.request.Request(url, data=body, headers={'content-type': ctype}, method='POST')
        try:
            with urllib.request.urlopen(req, timeout=timeout) as r:
                out = json.loads(r.read().decode('utf-8'))
        except urllib.error.HTTPError as e:
            raise RuntimeError(f'Telegram {method}: HTTP {e.code}') from None
        except (urllib.error.URLError, OSError, ValueError) as e:
            raise RuntimeError(f'Telegram {method}: {type(e).__name__}') from None
        if not out.get('ok'):
            raise RuntimeError(f'Telegram {method}: {out.get("description", "error")}')
        return out['result']

    def download(self, file_id):
        info = self.api('getFile', {'file_id': file_id})
        url = f'{API_BASE}/file/bot{self.token}/{info["file_path"]}'
        try:
            with urllib.request.urlopen(url, timeout=60) as r:
                data = r.read(MAX_VOICE_BYTES + 1)
        except (urllib.error.URLError, OSError) as e:
            raise RuntimeError(f'Telegram download: {type(e).__name__}') from None
        if len(data) > MAX_VOICE_BYTES:
            raise RuntimeError('голосовое слишком большое')
        return data

    def say(self, chat, text):
        try:
            self.api('sendMessage', {'chat_id': chat, 'text': text})
        except RuntimeError as e:
            self.log(e)

    # --- from the board to Telegram ---
    def send_recording(self, wav_path):
        if self.chat_id is None:
            return
        threading.Thread(target=self._send_recording, args=(wav_path,), daemon=True).start()

    def _send_recording(self, wav_path):
        name = os.path.basename(wav_path)
        try:
            ogg = wav_to_ogg_opus(wav_path)
            if ogg:
                self.api('sendVoice', {'chat_id': self.chat_id}, {'voice': ('voice.ogg', ogg, 'audio/ogg')}, timeout=120)
            else:
                with open(wav_path, 'rb') as f:
                    self.api('sendDocument', {'chat_id': self.chat_id, 'caption': 'запись с платы'},
                             {'document': (name, f.read(), 'audio/wav')}, timeout=120)
            self.log('telegram: sent', name)
        except (RuntimeError, OSError, subprocess.SubprocessError) as e:
            self.log('telegram: could not send', name, '-', e)

    # --- from Telegram to the board ---
    def handle(self, msg):
        chat = msg['chat']['id']
        if self.chat_id is None:                       # pairing mode
            self.say(chat, f'Ваш числовой ID: {chat}\nВпишите его в telegram.json (chat_id) и перезапустите сервер.')
            self.log('telegram: pairing request from chat', chat)
            return
        if chat != self.chat_id:
            return                                     # strangers get no answer
        media = msg.get('voice') or msg.get('audio') or msg.get('video_note')
        doc = msg.get('document')
        if doc and str(doc.get('mime_type', '')).startswith('audio'):
            media = doc
        if not media:
            self.say(chat, 'Пришлите голосовое сообщение: оно будет ждать на колонке платы.')
            return
        try:
            pcm = decode_to_pcm(self.download(media['file_id']))
        except (RuntimeError, OSError, wave.Error, subprocess.SubprocessError) as e:
            self.say(chat, f'Не получилось принять: {e}')
            self.log('telegram: voice failed -', e)
            return
        name = datetime.now().strftime('tg_%Y%m%d_%H%M%S.wav')
        with wave.open(os.path.join(self.rec_dir, name), 'wb') as w:
            w.setnchannels(1)
            w.setsampwidth(2)
            w.setframerate(RATE)
            w.writeframes(pcm)
        self.log('telegram: voice saved', name, f'{len(pcm) / 2 / RATE:.1f} s')
        self.on_new()
        self.say(chat, f'Принято ({len(pcm) / 2 / RATE:.0f} с): ждёт на колонке платы.')

    def run(self):
        offset = 0
        try:
            with open(self.offset_file) as f:
                offset = int(f.read().strip())
        except (OSError, ValueError):
            pass
        self.log('telegram: bridge started', 'paired' if self.chat_id is not None else 'pairing mode (no chat_id yet)')
        while not self.stop:
            try:
                updates = self.api('getUpdates', {'offset': offset, 'timeout': 25, 'allowed_updates': ['message']}, timeout=40)
            except RuntimeError as e:
                self.log(e)
                time.sleep(5)
                continue
            for u in updates:
                offset = u['update_id'] + 1
                try:
                    if 'message' in u:
                        self.handle(u['message'])
                except Exception as e:                   # one bad message must not stop the bridge
                    self.log('telegram: error -', type(e).__name__)
                with open(self.offset_file, 'w') as f:
                    f.write(str(offset))


def start(rec_dir, on_new, log):
    """Start the bridge if telegram.json exists and has a token; returns the Bridge or None."""
    try:
        with open(CONFIG, encoding='utf-8') as f:
            cfg = json.load(f)
    except (OSError, ValueError):
        log('telegram: not configured (no telegram.json)')
        return None
    token = str(cfg.get('token', '')).strip()
    if not token or 'ВСТАВЬТЕ' in token:
        log('telegram: telegram.json has no token yet')
        return None
    b = Bridge(token, cfg.get('chat_id'), rec_dir, on_new, log)
    threading.Thread(target=b.run, daemon=True).start()
    return b
