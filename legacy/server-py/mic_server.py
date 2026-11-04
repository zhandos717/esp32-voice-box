#!/usr/bin/env python3
"""Microphone server. An ESP32 with an INMP441 streams 16-bit PCM over TCP (port 8800);
this saves it as WAV and serves a small web page on http://127.0.0.1:8801 (record / stop / play)."""
import asyncio, json, math, os, struct, threading, time, wave
from array import array
from datetime import datetime
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

try:
    import numpy as np
except ImportError:
    np = None

AUDIO_PORT = int(os.environ.get('MIC_AUDIO_PORT', '8800'))
WEB_PORT = int(os.environ.get('MIC_WEB_PORT', '8801'))
HERE = os.path.dirname(os.path.abspath(__file__))
REC_DIR = os.environ.get('MIC_REC_DIR') or os.path.join(HERE, 'recordings')   # tests use their own folder
GAIN = float(os.environ.get('MIC_GAIN', '4'))       # software gain applied to the saved WAV
os.makedirs(REC_DIR, exist_ok=True)

TG = None                      # the Telegram bridge, if telegram.json is filled in
lock = threading.Lock()
S = {'connected': False, 'peer': '', 'rate': 16000, 'recording': False, 'level': 0, 'last_audio': 0.0,
     'raw': None, 'rawpath': None, 'bytes': 0, 'started': 0.0, 'writer': None, 'last_file': None}
LOOP = None


def log(*a):
    print(datetime.now().strftime('%H:%M:%S'), *a, flush=True)


def rms16(data):
    if np is not None:
        x = np.frombuffer(data, dtype='<i2').astype(np.float64)
        return int(math.sqrt(float((x * x).mean()))) if x.size else 0
    a = array('h')
    a.frombytes(data)
    return int(math.sqrt(sum(v * v for v in a) / len(a))) if a else 0


def handle_audio(data):
    with lock:
        S['last_audio'] = time.time()
        S['level'] = rms16(data)
        if S['recording'] and S['raw']:
            S['raw'].write(data)
            S['bytes'] += len(data)


async def on_esp(reader, writer):
    peer = writer.get_extra_info('peername')
    try:
        hdr = await asyncio.wait_for(reader.readexactly(10), 10)
        if hdr[:4] != b'MIC2':
            raise ValueError('bad header (old firmware?)')
        rate, bits = struct.unpack('<IH', hdr[4:])
    except Exception as e:
        log('rejected', peer, e)
        writer.close()
        return
    with lock:
        S.update(connected=True, peer=f'{peer[0]}:{peer[1]}', rate=rate, writer=writer)
    log('ESP32 connected', peer, rate, 'Hz')
    writer.write(b'U' + bytes([min(255, len(unplayed_names()))]))
    # frames from the board: 1 byte type, 2 bytes length (little endian), payload
    #   type 1 = PCM audio, type 2 = the record button was pressed (toggle)
    try:
        while True:
            head = await reader.readexactly(3)
            kind, length = head[0], head[1] | (head[2] << 8)
            payload = await reader.readexactly(length) if length else b''
            if kind == 1:
                handle_audio(payload)
            elif kind == 2:
                log('button pressed')
                if S['recording']:
                    finish_recording()
                else:
                    start_recording()
            elif kind == 3:                       # the "play next" button on the board
                await play_next(writer)
    except (ConnectionError, asyncio.IncompleteReadError):
        pass
    finally:
        with lock:
            S.update(connected=False, writer=None, level=0)
        log('ESP32 disconnected')
        finish_recording()
        writer.close()


def send_cmd(b):
    w = S['writer']
    if w is None or LOOP is None:
        return False
    LOOP.call_soon_threadsafe(lambda: w.write(b))
    return True


# --- which recordings have already been listened to (kept in recordings/played.json) ---
PLAYED_FILE = os.path.join(REC_DIR, 'played.json')


def played_set():
    try:
        with open(PLAYED_FILE, encoding='utf-8') as f:
            return set(json.load(f))
    except (OSError, ValueError):
        return set()


def mark_played(name):
    with lock:
        p = played_set()
        p.add(name)
        with open(PLAYED_FILE, 'w', encoding='utf-8') as f:
            json.dump(sorted(p), f)


def all_wavs():
    # rec_20261103_214006.wav / tg_20261103_215331.wav: sort by the timestamp, not by the prefix
    return sorted((n for n in os.listdir(REC_DIR) if n.endswith('.wav')), key=lambda n: n.split('_', 1)[-1])


def unplayed_names():
    p = played_set()
    return [n for n in all_wavs() if n not in p]          # oldest first


def send_unplayed():
    """Tell the board how many recordings are still unheard: 'U' + one byte (the LED on the board)."""
    send_cmd(b'U' + bytes([min(255, len(unplayed_names()))]))


async def play_next(writer):
    """The board asked for the next unheard recording: send it as 'P' + length + PCM, or 'N' if there is none."""
    names = unplayed_names()
    if not names:
        writer.write(b'N')
        return
    name = names[0]
    with wave.open(os.path.join(REC_DIR, name), 'rb') as w:
        pcm = w.readframes(w.getnframes())
    log('playing on the board:', name, f'{len(pcm) / 32000:.1f} s')
    writer.write(b'P' + struct.pack('<I', len(pcm)) + pcm)
    await writer.drain()
    mark_played(name)
    writer.write(b'U' + bytes([min(255, len(unplayed_names()))]))


def start_recording():
    with lock:
        if not S['connected']:
            return {'ok': False, 'error': 'ESP32 не подключена'}
        if S['recording']:
            return {'ok': True}
        S['rawpath'] = os.path.join(REC_DIR, '.rec.raw')
        S['raw'] = open(S['rawpath'], 'wb')
        S['bytes'], S['started'], S['recording'] = 0, time.time(), True
    send_cmd(b'R')
    log('recording started')
    return {'ok': True}


def finish_recording():
    with lock:
        if not S['recording']:
            return None
        S['recording'] = False
        raw, path, rate = S['raw'], S['rawpath'], S['rate']
        S['raw'] = None
    raw.close()
    send_cmd(b'S')
    with open(path, 'rb') as f:
        data = f.read()
    os.remove(path)
    if len(data) < 2 * rate // 10:
        log('recording too short, dropped')
        return None
    name = datetime.now().strftime('rec_%Y%m%d_%H%M%S.wav')
    if np is not None:
        x = np.frombuffer(data, dtype='<i2').astype(np.float64)
        x = (x - x.mean()) * GAIN
        out = np.clip(x, -32768, 32767).astype('<i2').tobytes()
    else:
        a = array('h')
        a.frombytes(data)
        m = sum(a) / len(a)
        out = array('h', (max(-32768, min(32767, int((v - m) * GAIN))) for v in a)).tobytes()
    with wave.open(os.path.join(REC_DIR, name), 'wb') as w:
        w.setnchannels(1)
        w.setsampwidth(2)
        w.setframerate(rate)
        w.writeframes(out)
    S['last_file'] = name
    log('saved', name, f'{len(data) / 2 / rate:.1f} s')
    send_unplayed()
    if TG:
        TG.send_recording(os.path.join(REC_DIR, name))         # to the Telegram chat
    return name


PAGE = '''<!doctype html><html lang="ru"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Микрофон ESP32</title>
<style>
:root{--bg:#f4f5f6;--fg:#161a1d;--mut:#5d6870;--card:#fff;--line:#d9dee2;--acc:#0b7a6b;--rec:#c62f2f}
@media(prefers-color-scheme:dark){:root{--bg:#101416;--fg:#e6ecef;--mut:#92a1a9;--card:#171d20;--line:#2a343a;--acc:#4fcbb2;--rec:#ff6b6b}}
body{margin:0;background:var(--bg);color:var(--fg);font:15px/1.5 system-ui,Segoe UI,sans-serif}
main{max-width:640px;margin:0 auto;padding:20px 16px 48px;display:grid;gap:16px}
h1{font-size:22px;margin:0}.card{background:var(--card);border:1px solid var(--line);border-radius:8px;padding:16px;display:grid;gap:12px}
.row{display:flex;gap:12px;align-items:center;flex-wrap:wrap}.mut{color:var(--mut);font-size:13px}
button{font:inherit;font-weight:600;padding:10px 20px;border-radius:6px;border:1px solid var(--fg);background:var(--fg);color:var(--bg);cursor:pointer}
button.rec{background:var(--rec);border-color:var(--rec);color:#fff}button:disabled{opacity:.4;cursor:default}
.dot{width:10px;height:10px;border-radius:50%;background:var(--mut);display:inline-block;margin-right:6px}.dot.on{background:var(--acc)}
.bar{height:10px;background:var(--line);border-radius:5px;overflow:hidden}.bar i{display:block;height:100%;width:0;background:var(--acc);transition:width .12s}
audio{width:100%}.file{display:grid;gap:4px;padding-top:8px;border-top:1px solid var(--line)}
.new{color:var(--acc);font-size:12px;margin-left:6px}
</style>
<main>
<h1>Микрофон ESP32</h1>
<section class="card">
<div class="row"><span><i class="dot" id="dot"></i><span id="conn">ожидаю ESP32…</span></span><span class="mut" id="peer"></span></div>
<div class="bar"><i id="lvl"></i></div>
<div class="row"><button id="btn" disabled>Записать</button><span id="time" class="mut"></span></div>
</section>
<section class="card"><strong>Записи</strong><div id="list" class="mut">пока нет</div></section>
</main>
<script>
const $=id=>document.getElementById(id);let rec=false;
async function tick(){try{const s=await (await fetch('/api/status')).json();rec=s.recording;
$('dot').className='dot'+(s.connected?' on':'');$('conn').textContent=s.connected?'ESP32 подключена':'ожидаю ESP32…';
$('peer').textContent=s.connected?s.peer:'';$('lvl').style.width=Math.min(100,s.level/40)+'%';
$('btn').disabled=!s.connected;$('btn').textContent=rec?'Остановить':'Записать';$('btn').className=rec?'rec':'';
$('time').textContent=rec?('идёт запись '+s.seconds.toFixed(0)+' с'):'';}catch(e){}}
$('btn').onclick=async()=>{await fetch(rec?'/api/stop':'/api/start',{method:'POST'});tick();setTimeout(list,400)};
async function list(){const f=await (await fetch('/api/list')).json();
const el=$('list');if(el.contains(document.activeElement)||[...el.querySelectorAll('audio')].some(a=>!a.paused))return;
el.innerHTML=f.length?f.map(r=>`<div class="file"><span>${r.name}${r.played?'':' <b class="new">новая</b>'}</span><audio controls preload="none" src="/rec/${r.name}" data-n="${r.name}"></audio></div>`).join(''):'пока нет';
el.querySelectorAll('audio').forEach(a=>a.addEventListener('play',()=>fetch('/api/mark/'+a.dataset.n,{method:'POST'}),{once:true}))}
setInterval(tick,250);setInterval(list,4000);tick();list();
</script></html>'''


class H(BaseHTTPRequestHandler):
    def log_message(self, *a):
        pass

    def _send(self, code, body, ctype='application/json'):
        if isinstance(body, (dict, list)):
            body = json.dumps(body, ensure_ascii=False)
        if isinstance(body, str):
            body = body.encode('utf-8')
        self.send_response(code)
        text = ctype.startswith('text') or ctype == 'application/json'
        self.send_header('content-type', ctype + ('; charset=utf-8' if text else ''))
        self.send_header('content-length', str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        p = self.path.split('?')[0]
        if p == '/':
            return self._send(200, PAGE, 'text/html')
        if p == '/api/status':
            with lock:
                fresh = time.time() - S['last_audio'] < 1
                return self._send(200, {'connected': S['connected'], 'peer': S['peer'], 'recording': S['recording'],
                                        'level': S['level'] if fresh else 0,
                                        'seconds': time.time() - S['started'] if S['recording'] else 0})
        if p == '/api/list':
            done = played_set()
            return self._send(200, [{'name': n, 'played': n in done} for n in reversed(all_wavs())])
        if p.startswith('/rec/'):
            name = os.path.basename(p[5:])
            path = os.path.join(REC_DIR, name)
            if name.endswith('.wav') and os.path.isfile(path):
                with open(path, 'rb') as f:
                    return self._send(200, f.read(), 'audio/wav')
        self._send(404, {'error': 'not found'})

    def do_POST(self):
        p = self.path.split('?')[0]
        if p == '/api/start':
            return self._send(200, start_recording())
        if p == '/api/stop':
            return self._send(200, {'ok': True, 'file': finish_recording()})
        if p.startswith('/api/mark/'):                 # listened to in the browser
            name = os.path.basename(p[len('/api/mark/'):])
            if name in all_wavs():
                mark_played(name)
                send_unplayed()
                return self._send(200, {'ok': True})
        self._send(404, {'error': 'not found'})


def main():
    global LOOP, TG
    import telegram_bridge
    TG = telegram_bridge.start(REC_DIR, send_unplayed, log)     # a voice message from Telegram joins the queue
    web = ThreadingHTTPServer(('127.0.0.1', WEB_PORT), H)       # the web page is local only
    threading.Thread(target=web.serve_forever, daemon=True).start()
    LOOP = asyncio.new_event_loop()
    asyncio.set_event_loop(LOOP)
    LOOP.run_until_complete(asyncio.start_server(on_esp, '0.0.0.0', AUDIO_PORT))
    log(f'web page: http://127.0.0.1:{WEB_PORT}   audio port: {AUDIO_PORT}   gain x{GAIN}')
    try:
        LOOP.run_forever()
    except KeyboardInterrupt:
        pass


if __name__ == '__main__':
    main()
