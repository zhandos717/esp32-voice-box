"""Test of the Telegram bridge against a FAKE Bot API on localhost: no real token, no real Telegram, no real recordings."""
import io, json, math, os, socket, struct, subprocess, sys, threading, time, wave, shutil
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.join(HERE, 'run')                    # scratch folder of the test, never the real recordings
REC = os.path.join(ROOT, 'rec')
if os.path.isdir(REC):
    shutil.rmtree(REC)                              # only ever our own test folder
os.makedirs(REC, exist_ok=True)
DEFAULT_SERVER = [sys.executable, os.path.join(HERE, '..', 'server-py', 'mic_server.py')]

# ---- fake Telegram Bot API ----
calls, queue = [], []
def wav_bytes(sec=1.0):
    b = io.BytesIO()
    with wave.open(b, 'wb') as w:
        w.setnchannels(1); w.setsampwidth(2); w.setframerate(16000)
        w.writeframes(b''.join(struct.pack('<h', int(4000 * math.sin(2 * math.pi * 300 * i / 16000))) for i in range(int(16000 * sec))))
    return b.getvalue()
VOICE = wav_bytes(1.0)

# A real Telegram voice message is OGG/Opus. With ffmpeg available (project ffmpeg/ folder) the test sends one too.
FFMPEG = os.path.join(HERE, '..', 'ffmpeg', 'ffmpeg.exe')
HAVE_FF = os.path.isfile(FFMPEG)
OGG = b''
if HAVE_FF:
    tone = os.path.join(ROOT, 'tone2s.wav'); ogg = os.path.join(ROOT, 'tone2s.ogg')
    subprocess.run([FFMPEG, '-v', 'error', '-f', 'lavfi', '-i', 'sine=frequency=440:duration=2', '-ac', '1', '-ar', '16000', '-y', tone], check=True)
    subprocess.run([FFMPEG, '-v', 'error', '-i', tone, '-c:a', 'libopus', '-b:a', '24k', '-f', 'ogg', '-y', ogg], check=True)
    OGG = open(ogg, 'rb').read()

class Fake(BaseHTTPRequestHandler):
    def log_message(self, *a): pass
    def _json(self, obj):
        d = json.dumps(obj).encode(); self.send_response(200); self.send_header('content-type', 'application/json')
        self.send_header('content-length', str(len(d))); self.end_headers(); self.wfile.write(d)
    def do_GET(self):
        data = VOICE if self.path.endswith('F1.wav') else OGG if self.path.endswith('F2.ogg') else None
        if data is not None:
            self.send_response(200); self.send_header('content-length', str(len(data))); self.end_headers(); self.wfile.write(data)
    def do_POST(self):
        n = int(self.headers.get('content-length', 0)); body = self.rfile.read(n)
        m = self.path.split('/')[-1]
        if '/botTESTTOKEN/' not in self.path: return self._json({'ok': False, 'description': 'bad token'})
        if m == 'getUpdates':
            if queue: return self._json({'ok': True, 'result': [queue.pop(0)]})
            time.sleep(0.4); return self._json({'ok': True, 'result': []})
        if m == 'getFile':
            fid = json.loads(body.decode('utf-8')).get('file_id')
            return self._json({'ok': True, 'result': {'file_path': 'voice/F2.ogg' if fid == 'F2' else 'voice/F1.wav'}})
        calls.append((m, self.headers.get('content-type', '')[:20], len(body), body[:600]))
        self._json({'ok': True, 'result': {}})
srv = ThreadingHTTPServer(('127.0.0.1', 19090), Fake)
threading.Thread(target=srv.serve_forever, daemon=True).start()

def run_server(chat_id, thread_id=0):
    cfg = os.path.join(ROOT, 'telegram_test.json')
    json.dump({'token': 'TESTTOKEN', 'chat_id': chat_id, 'thread_id': thread_id}, open(cfg, 'w'))
    env = dict(os.environ, MIC_NO_DOTENV='1', MIC_REC_DIR=REC, MIC_AUDIO_PORT='18800', MIC_WEB_PORT='18801', TELEGRAM_CONFIG=cfg,
               TELEGRAM_API_BASE='http://127.0.0.1:19090', PYTHONUTF8='1')
    cmd = [os.environ['SERVER_EXE']] if os.environ.get('SERVER_EXE') else DEFAULT_SERVER
    print('TESTING:', cmd[0] if len(cmd) == 1 else cmd[-1])
    p = subprocess.Popen(cmd, env=env,
                         stdout=open(os.path.join(ROOT, 'srv.log'), 'w'), stderr=subprocess.STDOUT)
    time.sleep(2); return p

results = []
def check(name, ok, extra=''): results.append(ok); print(('PASS' if ok else 'FAIL'), name, extra)

# ---- phase A: pairing mode (no chat_id) ----
queue.append({'update_id': 1, 'message': {'chat': {'id': 555}, 'text': 'привет'}})
p = run_server(None); time.sleep(2)
msgs = [c for c in calls if c[0] == 'sendMessage']
check('pairing: the bot tells the chat its id', bool(msgs) and b'555' in msgs[0][3], f'({len(msgs)} message)')
p.kill(); p.wait(); calls.clear()

# ---- phase B: paired with chat 555 ----
queue.append({'update_id': 2, 'message': {'chat': {'id': 999}, 'voice': {'file_id': 'F1'}}})   # a stranger: must be ignored
queue.append({'update_id': 3, 'message': {'chat': {'id': 555}, 'message_thread_id': 8, 'voice': {'file_id': 'F1'}}})  # another topic: not ours
queue.append({'update_id': 4, 'message': {'chat': {'id': 555}, 'message_thread_id': 8, 'text': '/id'}})            # /id works in any topic
queue.append({'update_id': 5, 'message': {'chat': {'id': 555}, 'message_thread_id': 7, 'message_id': 4242, 'voice': {'file_id': 'F1'}}})  # the owner's topic
if HAVE_FF:
    queue.append({'update_id': 6, 'message': {'chat': {'id': 555}, 'message_thread_id': 7, 'voice': {'file_id': 'F2'}}})  # a real OGG/Opus voice
EXPECT = 1 + (1 if HAVE_FF else 0)
p = run_server(555, 7)
time.sleep(4)
files = sorted(os.listdir(REC)); tg = [f for f in files if f.startswith('tg_') and f.endswith('.wav')]
check(f'voices from the owner topic saved as tg_*.wav (expected {EXPECT}: only that topic)', len(tg) == EXPECT, str(files))
check('voice from a stranger or from another topic ignored', len(tg) == EXPECT)
if HAVE_FF:
    sizes = sorted(os.path.getsize(os.path.join(REC, f)) for f in tg)
    check('OGG/Opus voice decoded through ffmpeg (2 s = about 64000 bytes of PCM)', any(62000 < z < 70000 for z in sizes), str(sizes))
msgs = [json.loads(c[3].decode('utf-8', 'ignore') + '') if False else c for c in calls if c[0] == 'sendMessage']
ack_thread7 = [c for c in msgs if b'"message_thread_id": 7' in c[3] or b'"message_thread_id":7' in c[3]]
id_reply = [c for c in msgs if b'"message_thread_id": 8' in c[3] or b'"message_thread_id":8' in c[3]]
check('acknowledgement is posted into the owner topic (thread 7)', bool(ack_thread7), f'{len(msgs)} messages')
check('/id is answered in the topic it was asked in (thread 8)', bool(id_reply))

# fake board: connect, expect 'U' with the number of unheard recordings
s = socket.create_connection(('127.0.0.1', 18800)); s.sendall(b'MIC2' + struct.pack('<IH', 16000, 16)); s.settimeout(1.5)
time.sleep(0.6); first = s.recv(64)
check(f'board is told about {EXPECT} unheard recording(s)', first[:2] == bytes([85, EXPECT]), repr(first))
# an accidental tap (press and release within 0.1 s) must not leave a recording
before = sorted(f for f in os.listdir(REC) if f.endswith('.wav'))
s.sendall(bytes([2, 0, 0])); time.sleep(0.1)
for _ in range(7):
    pcm = b''.join(struct.pack('<h', 2000) for _ in range(256)); s.sendall(bytes([1, len(pcm) & 255, len(pcm) >> 8]) + pcm); time.sleep(0.016)
s.sendall(bytes([4, 0, 0])); time.sleep(1.0)
after = sorted(f for f in os.listdir(REC) if f.endswith('.wav'))
check('a short tap on the button is ignored', before == after, f'{len(before)} -> {len(after)} files')
# hold the button for ~1.5 s -> recording saved and sent to Telegram
s.sendall(bytes([2, 0, 0])); time.sleep(0.4)
for _ in range(90):
    pcm = b''.join(struct.pack('<h', 2000) for _ in range(256)); s.sendall(bytes([1, len(pcm) & 255, len(pcm) >> 8]) + pcm); time.sleep(0.016)
s.sendall(bytes([4, 0, 0])); time.sleep(2)
after2 = sorted(f for f in os.listdir(REC) if f.endswith('.wav'))
check('holding the button saves a recording', len(after2) == len(after) + 1, f'{len(after)} -> {len(after2)} files')
docs = [c for c in calls if c[0] in ('sendVoice', 'sendDocument')]
check('recording sent to the Telegram chat', bool(docs), docs[0][0] if docs else '')
if HAVE_FF:
    check('with ffmpeg the recording goes as a real voice message (sendVoice)', bool(docs) and docs[0][0] == 'sendVoice', docs[0][0] if docs else '')
check('the recording is posted into the topic (message_thread_id=7)',
      bool(docs) and b'message_thread_id' in docs[0][3] and b'\r\n7\r\n' in docs[0][3])
# play next on the board: should get the telegram voice or the recording (oldest first) as P+len+PCM
s.settimeout(3); s.sendall(bytes([3, 0, 0])); time.sleep(1.0)
data = b''
try:
    while True:
        d = s.recv(65536)
        if not d: break
        data += d
        if len(data) > 30000: break
except socket.timeout:
    pass
i = data.find(b'P')
n = struct.unpack('<I', data[i + 1:i + 5])[0] if i >= 0 else 0
check('board receives audio to play (P + length)', i >= 0 and n > 0, f'{n} bytes after {data[:i]!r}')
time.sleep(1.0)
react = [c for c in calls if c[0] == 'setMessageReaction' and b'4242' in c[3]]
check('after playing, the sender is told the voice was heard (reaction on message 4242)', bool(react))
check('oldest unheard first: the telegram voice (1.0 s = 32000 bytes) before the later recording', n == 32000, str(n))
s.close(); p.kill(); p.wait()
print('server log:'); print(open(os.path.join(ROOT, 'srv.log'), encoding='utf-8', errors='replace').read()[-700:])
print('ALL PASSED' if all(results) else 'SOME FAILED')
