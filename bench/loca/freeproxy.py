import json
import os
import re
import sys
import threading
import time
import urllib.error
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

UPSTREAM = "https://openrouter.ai/api/v1/chat/completions"
KEY_FILE = os.path.expanduser(os.environ.get("FREEPROXY_KEY_FILE", "~/.config/stratavell/openrouter.env"))
MIN_GAP = float(os.environ.get("FREEPROXY_MIN_GAP", "3.2"))
DAILY_CAP = int(os.environ.get("FREEPROXY_DAILY_CAP", "900"))
LOG = os.environ.get("FREEPROXY_LOG", "freeproxy.jsonl")
ACCOUNT = re.compile(rb'"user_id"\s*:\s*"[^"]*"')


def load_key():
    k = os.environ.get("OPENROUTER_API_KEY", "").strip()
    if not k:
        k = open(KEY_FILE).read().strip()
        if "=" in k:
            k = k.split("=", 1)[1].strip().strip("\"'")
    return k


KEY = load_key()
lock = threading.Lock()
last = [0.0]
day = ["", 0]
log_lock = threading.Lock()


def record(entry):
    with log_lock, open(LOG, "a") as f:
        f.write(json.dumps(entry) + "\n")


def count_today(today):
    n = 0
    if os.path.exists(LOG):
        for line in open(LOG):
            try:
                e = json.loads(line)
            except ValueError:
                continue
            if isinstance(e.get("status"), int) and time.strftime("%Y-%m-%d", time.gmtime(e["t"])) == today:
                n += 1
    return n


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *a):
        pass

    def reply(self, code, body):
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_POST(self):
        if self.path.rstrip("/") not in ("/v1/chat/completions", "/chat/completions"):
            return self.reply(404, b'{"error":{"message":"only chat completions are proxied"}}')
        raw = self.rfile.read(int(self.headers.get("Content-Length", "0")))
        try:
            req = json.loads(raw)
        except ValueError:
            return self.reply(400, b'{"error":{"message":"bad json"}}')
        model = str(req.get("model", ""))
        if not model.endswith(":free"):
            record({"t": time.time(), "model": model, "status": "refused"})
            return self.reply(403, b'{"error":{"message":"freeproxy: only :free models are allowed"}}')
        with lock:
            today = time.strftime("%Y-%m-%d", time.gmtime())
            if day[0] != today:
                day[0], day[1] = today, count_today(today)
            if day[1] >= DAILY_CAP:
                record({"t": time.time(), "day": today, "model": model, "status": "capped"})
                return self.reply(429, b'{"error":{"message":"freeproxy: daily request cap reached"}}')
            day[1] += 1
            wait = last[0] + MIN_GAP - time.time()
            if wait > 0:
                time.sleep(wait)
            last[0] = time.time()
        t0 = time.time()
        r = urllib.request.Request(UPSTREAM, data=raw, method="POST", headers={"Content-Type": "application/json", "Authorization": "Bearer " + KEY})
        try:
            with urllib.request.urlopen(r, timeout=900) as resp:
                code, body = resp.status, resp.read()
        except urllib.error.HTTPError as e:
            code, body = e.code, e.read()
        except Exception as e:
            code, body = 502, json.dumps({"error": {"message": "freeproxy upstream error: " + type(e).__name__}}).encode()
        body = ACCOUNT.sub(b'"user_id":"redacted"', body)
        usage = {}
        if code == 200:
            try:
                usage = json.loads(body).get("usage") or {}
            except ValueError:
                pass
        record({"t": t0, "day": time.strftime("%Y-%m-%d", time.gmtime(t0)), "model": model, "status": code, "seconds": round(time.time() - t0, 2), "messages": len(req.get("messages", [])), "tools": len(req.get("tools") or []), "prompt_tokens": usage.get("prompt_tokens"), "completion_tokens": usage.get("completion_tokens"), "cost": usage.get("cost"), "tag": self.headers.get("X-Run-Tag", "")})
        self.reply(code, body)


if __name__ == "__main__":
    port = int(sys.argv[1]) if len(sys.argv) > 1 else 18080
    ThreadingHTTPServer(("127.0.0.1", port), Handler).serve_forever()
