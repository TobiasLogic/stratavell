import json
import os
import socket
import http.client
import urllib.parse

__version__ = "0.1.3"

__all__ = ["Client", "Session", "Agent", "StratavellError", "text", "tool_call", "tool_result", "__version__"]


class _UnixConnection(http.client.HTTPConnection):
    def __init__(self, path, timeout=60):
        super().__init__("localhost", timeout=timeout)
        self._path = path

    def connect(self):
        s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        s.settimeout(self.timeout)
        s.connect(self._path)
        self.sock = s


class StratavellError(Exception):
    def __init__(self, status, body):
        super().__init__(f"HTTP {status}: {body.get('error', body)}")
        self.status = status
        self.code = body.get("code")
        self.body = body


class Client:
    def __init__(self, base_url="http://127.0.0.1:7077", socket_path=None, token=None, timeout=60):
        self.socket_path = socket_path
        self.url = urllib.parse.urlparse(base_url)
        self.token = token or os.environ.get("STRATAVELL_TOKEN")
        self.timeout = timeout

    def _conn(self):
        if self.socket_path:
            return _UnixConnection(self.socket_path, self.timeout)
        return http.client.HTTPConnection(self.url.hostname, self.url.port or 80, timeout=self.timeout)

    def request(self, method, path, body=None):
        headers = {"Content-Type": "application/json"}
        if self.token:
            headers["Authorization"] = "Bearer " + self.token
        conn = self._conn()
        try:
            conn.request(method, path, body=None if body is None else json.dumps(body), headers=headers)
            resp = conn.getresponse()
            data = json.loads(resp.read() or b"{}")
        finally:
            conn.close()
        if resp.status >= 400:
            raise StratavellError(resp.status, data)
        return data

    def session(self, session_id, project=""):
        self.request("POST", "/v1/sessions", {"id": session_id, "project": project})
        return Session(self, session_id)


class Session:
    def __init__(self, client, session_id):
        self.client = client
        self.id = session_id

    def agent(self, agent_id, parent=None, handoff=None):
        body = {"id": agent_id}
        if parent:
            body["parent"] = parent
            body["handoff"] = handoff or {}
        self.client.request("POST", f"/v1/sessions/{self.id}/agents", body)
        return Agent(self.client, self.id, agent_id)


class Agent:
    def __init__(self, client, session_id, agent_id):
        self.c = client
        self.base = f"/v1/sessions/{session_id}/agents/{agent_id}"

    def append(self, *messages):
        return self.c.request("POST", self.base + "/events", {"messages": list(messages)})["events"]

    def assemble(self, system="", tools=None, fmt="neutral", model="", max_tokens=0, include_memory_tools=True):
        return self.c.request("POST", self.base + "/assemble", {
            "system": system, "tools": tools or [], "format": fmt, "model": model,
            "max_tokens": max_tokens, "include_memory_tools": include_memory_tools,
        })

    def usage(self, usage):
        return self.c.request("POST", self.base + "/usage", usage)

    def tool(self, name, arguments):
        return self.c.request("POST", self.base + "/tools/" + name, {"input": arguments})

    def search(self, query, limit=8):
        q = urllib.parse.urlencode({"q": query, "limit": limit})
        return self.c.request("GET", self.base + "/search?" + q)["hits"]

    def event(self, event_id):
        return self.c.request("GET", self.base + "/events/" + event_id)


def text(role, s):
    return {"role": role, "parts": [{"type": "text", "text": s}]}


def tool_call(call_id, name, args, text_part=None):
    parts = []
    if text_part:
        parts.append({"type": "text", "text": text_part})
    parts.append({"type": "tool_call", "tool_call_id": call_id, "tool_name": name, "input": args})
    return {"role": "assistant", "parts": parts}


def tool_result(call_id, name, output, exit_code=None, command=None):
    meta = {}
    if exit_code is not None:
        meta["exit_code"] = exit_code
    if command:
        meta["command"] = command
    return {"role": "tool", "meta": meta,
            "parts": [{"type": "tool_result", "tool_call_id": call_id, "tool_name": name, "text": output, "is_error": bool(exit_code)}]}
