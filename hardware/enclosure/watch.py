"""Rebuild the STL files when enclosure.scad changes (hot reload for the model).

Run:  python hardware/enclosure/watch.py
The usta editor polls print_bambu.stl and reloads it when it changes.
"""
import os
import subprocess
import time

HERE = os.path.dirname(os.path.abspath(__file__))
OPENSCAD = r'C:\Program Files\OpenSCAD\openscad.exe'
SRC = os.path.join(HERE, 'enclosure.scad')
PARTS = {'print': 'print_bambu.stl', 'body': 'body.stl', 'lid': 'lid.stl'}   # the viewer file first


def build():
    t0 = time.time()
    for part, out in PARTS.items():
        r = subprocess.run([OPENSCAD, '-D', f'part="{part}"', '-o', os.path.join(HERE, out), SRC],
                           capture_output=True, text=True)
        if r.returncode != 0 or 'WARNING' in r.stderr:
            print(f'{part}: problem\n{r.stderr.strip()}')
    print(f'rebuilt in {time.time() - t0:.1f} s')


last = 0.0
while True:
    stamp = os.path.getmtime(SRC)
    if stamp != last:
        last = stamp
        build()
    time.sleep(0.7)
