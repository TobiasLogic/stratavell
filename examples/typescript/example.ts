import { Client, text } from "../../clients/typescript/src/index.ts";

const agent = await new Client(process.env.STRATAVELL_URL ?? "http://127.0.0.1:7077").agent("ts-demo", "main");

await agent.append(
  text("user", "Upgrade the HTTP client. Keep Node 18 support; do not add dependencies."),
  {
    role: "assistant",
    parts: [
      { type: "text", text: "Checking current usage." },
      { type: "tool_call", tool_call_id: "c1", tool_name: "grep", input: { pattern: "axios" } },
    ],
  },
  { role: "tool", parts: [{ type: "tool_result", tool_call_id: "c1", tool_name: "grep", text: "src/api.ts:3:import axios from 'axios'\nsrc/upload.ts:9:axios.post(url, body)" }] },
);

const ctx = await agent.assemble({ system: "You are a TypeScript maintainer.", format: "openai", model: "gpt-5-mini" });
console.log(`tokens ${ctx.tokens} of ${ctx.capacity}, ${ctx.request.messages.length} chat messages, state ${ctx.report.state}`);

const { hits } = await agent.search("axios import");
for (const h of hits) console.log("hit", h.id, h.provenance, h.snippet.slice(0, 70));

const ts = await agent.tool("memory_get_task_state", {});
console.log(ts.text);
