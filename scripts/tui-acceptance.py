"""Runs `<binary> tui` in a pty at 120x40 and measures it (tui-acceptance.sh).

Usage: tui-acceptance.py idle|flood|wide BINARY SECONDS [SERVER]

idle measures the CPU over SECONDS after a warm-up; flood and wide first
select SERVER and focus its logs (wide also turns wrapping on). Each then
presses `?` twenty times and reports how long the help took to appear.
With pyte installed it also prints the last screen.
"""

import os
import pty
import re
import select
import signal
import statistics
import struct
import subprocess
import sys
import threading
import time
import fcntl
import termios

COLS, ROWS = 120, 40
ESCAPES = re.compile(rb"\x1b\[[0-9;:?<>=]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b[()][0-9A-Za-z]|\x1b[=>78]")


class TUI:
    def __init__(self, binary):
        self.pid, self.fd = pty.fork()
        if self.pid == 0:
            os.environ.update(TERM="xterm-256color", COLORTERM="truecolor", LANG="en_US.UTF-8")
            os.execv(binary, [binary, "tui"])
        fcntl.ioctl(self.fd, termios.TIOCSWINSZ, struct.pack("HHHH", ROWS, COLS, 0, 0))
        self.raw = bytearray()
        self.text = bytearray()  # the output without escape sequences
        self.lock = threading.Lock()
        self.alive = True
        threading.Thread(target=self._read, daemon=True).start()

    def _read(self):
        while self.alive:
            r, _, _ = select.select([self.fd], [], [], 0.05)
            if not r:
                continue
            try:
                data = os.read(self.fd, 1 << 16)
            except OSError:
                return
            if not data:
                return
            with self.lock:
                self.raw += data
                self.text += ESCAPES.sub(b"", data)

    def clears(self):
        """Erases of the whole screen after the first frame: a renderer
        that redraws only what changed needs none, and each one can show
        as a flicker."""
        with self.lock:
            raw = bytes(self.raw)
        first = raw.find(b"Servers")
        return len(re.findall(rb"\x1b\[2J|\x1b\[H\x1b\[J|\x1b\[1;1H\x1b\[J", raw[first:]))

    def mark(self):
        with self.lock:
            return len(self.text)

    def wait_for(self, needle, since=0, timeout=10.0):
        """Seconds until needle shows in the output after since, or None."""
        start = time.monotonic()
        while time.monotonic() - start < timeout:
            with self.lock:
                if needle in self.text[since:]:
                    return time.monotonic() - start
            time.sleep(0.001)
        return None

    def send(self, keys, pause=0.4):
        os.write(self.fd, keys.encode())
        time.sleep(pause)

    def cpu(self):
        """CPU seconds the program used so far (user and system)."""
        stat = f"/proc/{self.pid}/stat"
        if os.path.exists(stat):
            with open(stat) as f:
                fields = f.read().rsplit(")", 1)[1].split()
            return (int(fields[11]) + int(fields[12])) / os.sysconf("SC_CLK_TCK")
        out = subprocess.run(["ps", "-o", "time=", "-p", str(self.pid)], capture_output=True, text=True).stdout.strip()
        seconds = 0.0
        for part in out.split(":"):
            seconds = seconds * 60 + float(part)
        return seconds

    def screen(self):
        try:
            import pyte
        except ImportError:
            return None
        class Screen(pyte.Screen):
            # The renderer moves left with CBT, which pyte lacks.
            def cursor_back_tab(self, count=None):
                for _ in range(count or 1):
                    stops = [x for x in self.tabstops if x < self.cursor.x]
                    self.cursor.x = max(stops) if stops else 0

        class Stream(pyte.ByteStream):
            csi = dict(pyte.ByteStream.csi, Z="cursor_back_tab")

        s = Screen(COLS, ROWS)
        stream = Stream(s)
        with self.lock:
            stream.feed(bytes(self.raw))
        return "\n".join(line.rstrip() for line in s.display)

    def quit(self):
        os.write(self.fd, b"q")
        for _ in range(50):
            pid, _ = os.waitpid(self.pid, os.WNOHANG)
            if pid:
                break
            time.sleep(0.1)
        else:
            os.kill(self.pid, signal.SIGTERM)
            os.waitpid(self.pid, 0)
        self.alive = False


def help_latency(tui, times=20):
    """Milliseconds from `?` to the help on screen, for each press."""
    out = []
    for _ in range(times):
        since = tui.mark()
        start = time.monotonic()
        os.write(tui.fd, b"?")
        took = tui.wait_for(b"Keys", since, timeout=5)
        out.append(None if took is None else (time.monotonic() - start) * 1000)
        tui.send("\x1b", 0.3)
    return out


def cpu_over(tui, seconds):
    c0, t0 = tui.cpu(), time.monotonic()
    time.sleep(seconds)
    c1, t1 = tui.cpu(), time.monotonic()
    return c1 - c0, t1 - t0


def main():
    mode, binary, seconds = sys.argv[1], sys.argv[2], float(sys.argv[3])
    server = sys.argv[4] if len(sys.argv) > 4 else ""
    tui = TUI(binary)
    if tui.wait_for(b"Servers", timeout=15) is None:
        print(f"{mode}: no first frame", file=sys.stderr)
        sys.exit(1)
    time.sleep(8 if mode == "idle" else 2)
    if server:
        tui.send("/")
        tui.send(server)
        tui.send("\r")
        tui.send("\r", 1.0)
        if mode == "wide":
            tui.send("w", 1.0)
    used, wall = cpu_over(tui, seconds)
    lat = help_latency(tui)
    shown = [x for x in lat if x is not None]
    screen = tui.screen()
    clears = tui.clears()
    tui.quit()
    pct = 100 * used / wall
    print(f"{mode}: {used:.2f} CPU s in {wall:.1f} s = {pct:.2f}% of one core")
    if shown:
        q = statistics.quantiles(shown, n=20) if len(shown) > 1 else shown
        print(f"{mode}: `?` help shown {len(shown)}/{len(lat)}: median {statistics.median(shown):.0f} ms, p95 {q[-1]:.0f} ms, max {max(shown):.0f} ms")
    else:
        print(f"{mode}: `?` never showed the help")
    print(f"{mode}: full-screen erases after the first frame: {clears}")
    if screen and os.environ.get("SHOW_SCREEN"):
        print(screen)


if __name__ == "__main__":
    main()
