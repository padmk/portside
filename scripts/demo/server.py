"""Fake dev server for screenshots and demos: prints framework-like logs and
optionally serves HTTP. Usage: server.py --kind next|vite|api|worker|docs --name NAME [--port N] [--crash-after S] [--mem MB]"""
import argparse, http.server, random, socketserver, sys, threading, time

ap = argparse.ArgumentParser()
ap.add_argument("--kind", default="api")
ap.add_argument("--name", default="app")
ap.add_argument("--port", type=int, default=0)
ap.add_argument("--crash-after", type=float, default=0)
ap.add_argument("--mem", type=int, default=0, help="resident memory to hold, so usage looks like a real dev server")
a = ap.parse_args()

G, Y, R, D, C, M, B, X = "\033[32m", "\033[33m", "\033[31m", "\033[2m", "\033[36m", "\033[35m", "\033[1m", "\033[0m"
random.seed(a.name)
say = lambda s: print(s, flush=True)

# Touch every page so the memory counts as resident.
ballast = bytearray(a.mem * 1024 * 1024)
for i in range(0, len(ballast), 4096):
    ballast[i] = 1


def work(ms):
    """Burn a little CPU per request so the CPU sparkline is not flat."""
    end = time.perf_counter() + ms / 1000
    while time.perf_counter() < end:
        pass


if a.kind == "next":
    say(f"   {M}{B}▲ Next.js 16.2.1{X}")
    say(f"   - Local:        http://127.0.0.1:{a.port}")
    say(f" {G}✓{X} Starting...")
    time.sleep(1.2)
    say(f" {G}✓{X} Ready in 1180ms")
elif a.kind == "vite":
    time.sleep(0.6)
    say(f"  {G}{B}VITE v7.1.4{X}  ready in {D}412 ms{X}")
    say(f"  {G}➜{X}  {B}Local{X}:   {C}http://127.0.0.1:{a.port}/{X}")
elif a.kind == "docs":
    say(f"{C}astro{X}  v5.13.0 {G}ready in 538 ms{X}")
    say(f"┃ Local    {C}http://127.0.0.1:{a.port}/{X}")
elif a.kind == "api":
    say(f"{G}INFO{X}:     Started server process [{random.randint(20000, 60000)}]")
    say(f"{G}INFO{X}:     Waiting for application startup.")
    time.sleep(0.8)
    say(f"{G}INFO{X}:     Uvicorn running on {B}http://127.0.0.1:{a.port}{X} (Press CTRL+C to quit)")
else:
    say(f"{D}[worker]{X} connected to queue {B}emails{X}")

if a.port:
    class H(http.server.BaseHTTPRequestHandler):
        def do_GET(self):
            self.send_response(200)
            self.send_header("content-type", "text/html")
            self.end_headers()
            self.wfile.write(f"<h1>{a.name}</h1>".encode())
        def log_message(self, *args):
            pass
    socketserver.TCPServer.allow_reuse_address = True
    srv = socketserver.ThreadingTCPServer(("127.0.0.1", a.port), H)
    threading.Thread(target=srv.serve_forever, daemon=True).start()

routes = {
    "next": ["/", "/pricing", "/dashboard", "/api/session", "/settings"],
    "vite": ["src/App.tsx", "src/routes/orders.tsx", "src/components/Chart.tsx"],
    "api": ["/v1/orders", "/v1/customers", "/v1/health", "/v1/invoices"],
    "docs": ["/guides/getting-started/", "/reference/cli/", "/"],
    "worker": ["welcome", "receipt", "password-reset"],
}[a.kind if a.kind in ("next", "vite", "api", "docs") else "worker"]
routes = {
    "blog": ["/", "/posts/shipping-v2/", "/posts/postgres-tips/", "/tags/go/", "/feed.xml"],
    "storefront": ["/", "/products/", "/cart", "/checkout"],
}.get(a.name, routes)
start = time.time()
while True:
    time.sleep(random.uniform(0.6, 1.6))
    if a.crash_after and time.time() - start > a.crash_after:
        say(f"{R}Error{X}: Missing environment variable CONTENT_API_TOKEN")
        say(f"    at loadPosts (src/lib/content.ts:14:11)")
        say(f"    at async Page (src/app/page.tsx:8:17)")
        sys.exit(1)
    r, path, ms = random.random(), random.choice(routes), random.randint(4, 120)
    work(ms / 4)
    t = time.strftime("%H:%M:%S")
    if a.kind == "next":
        say(f" GET {path} {G if r > 0.08 else Y}{200 if r > 0.08 else 404}{X} in {ms}ms" if r > 0.15 else f" {G}✓{X} Compiled {path} in {ms * 3}ms")
    elif a.kind == "vite":
        say(f"{D}{t}{X} {C}{B}[vite]{X} {G}hmr update{X} {D}/{path}{X}")
    elif a.kind == "api":
        code = 200 if r > 0.1 else 422
        say(f"{G}INFO{X}:     127.0.0.1:{random.randint(50000, 65000)} - \"GET {path} HTTP/1.1\" {G if code == 200 else Y}{code}{X}")
    elif a.kind == "docs":
        say(f"{D}{t}{X} {C}[200]{X} {path} {D}{ms}ms{X}")
    else:
        say(f"{D}{t}{X} sent {B}{path}{X} to customer #{random.randint(1000, 9999)} {D}({ms}ms){X}")
