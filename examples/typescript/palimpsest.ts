export type Part = {
  type: "text" | "tool_call" | "tool_result" | "thinking" | "redacted_thinking" | "opaque";
  text?: string;
  tool_call_id?: string;
  tool_name?: string;
  input?: unknown;
  is_error?: boolean;
  signature?: string;
  data?: string;
};

export type Message = { role: "user" | "assistant" | "tool"; parts: Part[]; meta?: Record<string, unknown> };

export class PalimpsestError extends Error {
  status: number;
  code: string;
  constructor(status: number, code: string, message: string) {
    super(message);
    this.status = status;
    this.code = code;
  }
}

export class Client {
  baseUrl: string;
  token: string | undefined;
  constructor(baseUrl = "http://127.0.0.1:7077", token = process.env.PALIMPSEST_TOKEN) {
    this.baseUrl = baseUrl;
    this.token = token;
  }

  async request<T>(method: string, path: string, body?: unknown): Promise<T> {
    const headers: Record<string, string> = { "content-type": "application/json" };
    if (this.token) headers.authorization = `Bearer ${this.token}`;
    const res = await fetch(this.baseUrl + path, { method, headers, body: body === undefined ? undefined : JSON.stringify(body) });
    const data = (await res.json()) as any;
    if (!res.ok) throw new PalimpsestError(res.status, data.code, data.error);
    return data as T;
  }

  async agent(session: string, agent: string): Promise<Agent> {
    await this.request("POST", "/v1/sessions", { id: session });
    await this.request("POST", `/v1/sessions/${session}/agents`, { id: agent });
    return new Agent(this, `/v1/sessions/${session}/agents/${agent}`);
  }
}

export class Agent {
  c: Client;
  base: string;
  constructor(c: Client, base: string) {
    this.c = c;
    this.base = base;
  }

  append(...messages: Message[]) {
    return this.c.request<{ events: { id: string; seq: number; tokens: number }[] }>("POST", `${this.base}/events`, { messages });
  }

  assemble(opts: { system?: string; tools?: unknown[]; format?: "neutral" | "anthropic" | "openai"; model?: string; max_tokens?: number }) {
    return this.c.request<any>("POST", `${this.base}/assemble`, { include_memory_tools: true, ...opts });
  }

  usage(u: { input_tokens: number; output_tokens: number; cache_read_input_tokens?: number; cache_creation_input_tokens?: number }) {
    return this.c.request("POST", `${this.base}/usage`, u);
  }

  tool(name: string, input: unknown) {
    return this.c.request<{ text: string; is_error: boolean }>("POST", `${this.base}/tools/${name}`, { input });
  }

  search(q: string, limit = 8) {
    return this.c.request<{ hits: any[] }>("GET", `${this.base}/search?q=${encodeURIComponent(q)}&limit=${limit}`);
  }
}

export const text = (role: "user" | "assistant", t: string): Message => ({ role, parts: [{ type: "text", text: t }] });
