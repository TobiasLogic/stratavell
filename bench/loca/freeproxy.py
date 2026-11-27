import json
import os
import re
import sys
import threading
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

PROVIDERS = {
    "openrouter": ("https://openrouter.ai/api/v1/chat/completions", "~/.config/stratavell/openrouter.env", "OPENROUTER_API_KEY"),
    "nvidia": ("https://integrate.api.nvidia.com/v1/chat/completions", "~/.config/stratavell/nvidia.env", "NVIDIA_API_KEY"),
}
PROVIDER = os.environ.get("FREEPROXY_UPSTREAM", "openrouter")
UPSTREAM, DEFAULT_KEY_FILE, KEY_ENV = PROVIDERS[PROVIDER]
STREAM = PROVIDER == "nvidia"
KEY_FILE = os.path.expanduser(os.environ.get("FREEPROXY_KEY_FILE", DEFAULT_KEY_FILE))
MIN_GAP = float(os.environ.get("FREEPROXY_MIN_GAP", "3.2"))
DAILY_CAP = int(os.environ.get("FREEPROXY_DAILY_CAP", "900"))
RETRY_429 = int(os.environ.get("FREEPROXY_RETRY_429", "40" if STREAM else "0"))
RETRY_NET_SECONDS = float(os.environ.get("FREEPROXY_RETRY_NET_SECONDS", "900" if STREAM else "0"))
NET_WAIT = 30
READY_TIMEOUT = float(os.environ.get("FREEPROXY_READY_TIMEOUT", "120"))
TRANSIENT = (500, 502, 503, 504)
LOG = os.environ.get("FREEPROXY_LOG", "freeproxy.jsonl")
ACCOUNT = re.compile(rb'"user_id"\s*:\s*"[^"]*"')


def load_key():
    k = os.environ.get(KEY_ENV, "").strip()
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
    entry["provider"] = PROVIDER
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


def collect(resp):
    content, reasoning, calls = [], [], {}
    finish, usage, rid, model, created = None, None, None, None, None
    for raw in resp:
        line = raw.decode("utf-8", "replace").strip()
        if not line.startswith("data:"):
            continue
        data = line[5:].strip()
        if data == "[DONE]":
            break
        d = json.loads(data)
        if d.get("error"):
            raise RuntimeError("stream error: " + json.dumps(d["error"])[:300])
        rid = rid or d.get("id")
        model = model or d.get("model")
        created = created or d.get("created")
        if d.get("usage"):
            usage = d["usage"]
        for ch in d.get("choices") or []:
            delta = ch.get("delta") or {}
            if delta.get("content"):
                content.append(delta["content"])
            r = delta.get("reasoning_content") or delta.get("reasoning")
            if r:
                reasoning.append(r)
            for tc in delta.get("tool_calls") or []:
                c = calls.setdefault(tc.get("index", len(calls)), {"id": None, "type": "function", "function": {"name": "", "arguments": ""}})
                c["id"] = c["id"] or tc.get("id")
                f = tc.get("function") or {}
                c["function"]["name"] += f.get("name") or ""
                c["function"]["arguments"] += f.get("arguments") or ""
            if ch.get("finish_reason"):
                finish = ch["finish_reason"]
    message = {"role": "assistant", "content": "".join(content) or (None if calls else ""), "reasoning": "".join(reasoning) or None}
    if calls:
        for i, c in sorted(calls.items()):
            c["id"] = c["id"] or "call_" + uuid.uuid4().hex[:24]
        message["tool_calls"] = [c for _, c in sorted(calls.items())]
    return {
        "id": rid or "chatcmpl-" + uuid.uuid4().hex,
        "object": "chat.completion",
        "created": created or int(time.time()),
        "model": model,
        "provider": PROVIDER,
        "choices": [{"index": 0, "message": message, "finish_reason": finish}],
        "usage": usage or {},
    }


def upstream(raw, timeout=900):
    r = urllib.request.Request(UPSTREAM, data=raw, method="POST", headers={"Content-Type": "application/json", "Authorization": "Bearer " + KEY, "Accept": "text/event-stream" if STREAM else "application/json"})
    try:
        with urllib.request.urlopen(r, timeout=timeout) as resp:
            if STREAM:
                return 200, json.dumps(collect(resp)).encode(), False
            return resp.status, resp.read(), False
    except urllib.error.HTTPError as e:
        return e.code, e.read(), e.code in TRANSIENT
    except Exception as e:
        return 502, json.dumps({"error": {"message": "freeproxy upstream error: " + type(e).__name__ + ": " + str(e)[:200]}}).encode(), True


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *a):
        pass

    def reply(self, code, body):
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        url = urllib.parse.urlparse(self.path)
        if url.path.rstrip("/") != "/ready":
            return self.reply(404, b'{"error":{"message":"only /ready is served"}}')
        model = (urllib.parse.parse_qs(url.query).get("model") or [""])[0]
        if not model.endswith(":free"):
            return self.reply(403, b'{"error":{"message":"freeproxy: only :free models are allowed"}}')
        req = {"model": model[: -len(":free")] if STREAM else model, "messages": [{"role": "user", "content": "Reply with the single word: ready"}], "max_tokens": 16}
        if STREAM:
            req["stream"] = True
            req["stream_options"] = {"include_usage": True}
        with lock:
            wait = last[0] + MIN_GAP - time.time()
            if wait > 0:
                time.sleep(wait)
            last[0] = time.time()
        t0 = time.time()
        code, body, _ = upstream(json.dumps(req).encode(), timeout=READY_TIMEOUT)
        record({"t": t0, "day": time.strftime("%Y-%m-%d", time.gmtime(t0)), "model": model, "status": code, "seconds": round(time.time() - t0, 2), "tag": "ready"})
        return self.reply(200 if code == 200 else 503, json.dumps({"ready": code == 200, "status": code}).encode())

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
        if req.get("stream"):
            return self.reply(400, b'{"error":{"message":"freeproxy: streaming clients are not supported"}}')
        if STREAM:
            req["model"] = model[: -len(":free")]
            req["stream"] = True
            req["stream_options"] = {"include_usage": True}
            raw = json.dumps(req).encode()
        attempt = rate_tries = 0
        first = time.time()
        while True:
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
            code, body, transient = upstream(raw)
            body = ACCOUNT.sub(b'"user_id":"redacted"', body)
            usage, finish, error = {}, None, None
            try:
                parsed = json.loads(body)
            except ValueError:
                parsed = {}
            if code == 200:
                usage = parsed.get("usage") or {}
                finish = ((parsed.get("choices") or [{}])[0]).get("finish_reason")
            else:
                error = str((parsed.get("error") or {}).get("message") or body[:200])[:200]
            record({"t": t0, "day": time.strftime("%Y-%m-%d", time.gmtime(t0)), "model": model, "status": code, "attempt": attempt, "seconds": round(time.time() - t0, 2), "messages": len(req.get("messages", [])), "tools": len(req.get("tools") or []), "prompt_tokens": usage.get("prompt_tokens"), "completion_tokens": usage.get("completion_tokens"), "reasoning_tokens": (usage.get("completion_tokens_details") or {}).get("reasoning_tokens"), "cached_tokens": (usage.get("prompt_tokens_details") or {}).get("cached_tokens"), "finish": finish, "error": error, "cost": usage.get("cost"), "tag": self.headers.get("X-Run-Tag", "")})
            if code == 429 and rate_tries < RETRY_429:
                attempt += 1
                rate_tries += 1
                time.sleep(min(10 * 2 ** min(rate_tries, 4), 120))
                continue
            if transient and time.time() + NET_WAIT - first < RETRY_NET_SECONDS:
                attempt += 1
                time.sleep(NET_WAIT)
                continue
            return self.reply(code, body)


if __name__ == "__main__":
    port = int(sys.argv[1]) if len(sys.argv) > 1 else 18080
    ThreadingHTTPServer(("127.0.0.1", port), Handler).serve_forever()
