use serde_json::{json, Value};
use std::env;

struct Agent {
    base: String,
}

impl Agent {
    fn connect(url: &str, session: &str, agent: &str) -> Result<Agent, ureq::Error> {
        ureq::post(&format!("{url}/v1/sessions")).send_json(json!({ "id": session }))?;
        ureq::post(&format!("{url}/v1/sessions/{session}/agents")).send_json(json!({ "id": agent }))?;
        Ok(Agent { base: format!("{url}/v1/sessions/{session}/agents/{agent}") })
    }

    fn post(&self, path: &str, body: Value) -> Result<Value, ureq::Error> {
        Ok(ureq::post(&format!("{}{}", self.base, path)).send_json(body)?.into_json()?)
    }

    fn get(&self, path: &str) -> Result<Value, ureq::Error> {
        Ok(ureq::get(&format!("{}{}", self.base, path)).call()?.into_json()?)
    }
}

fn main() -> Result<(), Box<dyn std::error::Error>> {
    let url = env::var("STRATAVELL_URL").unwrap_or_else(|_| "http://127.0.0.1:7077".into());
    let agent = Agent::connect(&url, "rust-demo", "main")?;

    agent.post("/events", json!({ "messages": [
        { "role": "user", "parts": [{ "type": "text", "text": "Make the parser zero-copy. Keep the public API unchanged." }] },
        { "role": "assistant", "parts": [
            { "type": "text", "text": "Running the benchmarks first." },
            { "type": "tool_call", "tool_call_id": "c1", "tool_name": "bash", "input": { "command": "cargo bench" } }
        ]},
        { "role": "tool", "meta": { "command": "cargo bench", "exit_code": 0 }, "parts": [
            { "type": "tool_result", "tool_call_id": "c1", "tool_name": "bash", "text": "parse_small  time: [812.4 ns 815.0 ns 818.1 ns]\nparse_large  time: [92.1 us 92.6 us 93.2 us]" }
        ]}
    ]}))?;

    let ctx = agent.post("/assemble", json!({ "system": "You are a Rust performance engineer.", "format": "anthropic", "model": "claude-sonnet-5-5", "max_tokens": 4096, "include_memory_tools": true }))?;
    println!("tokens {} of {}, state {}", ctx["tokens"], ctx["capacity"], ctx["report"]["state"]);

    let hits = agent.get("/search?q=parse_large%20bench&limit=3")?;
    for h in hits["hits"].as_array().into_iter().flatten() {
        println!("hit {} {}", h["id"], h["provenance"]);
    }
    let ts = agent.post("/tools/memory_get_task_state", json!({ "input": {} }))?;
    println!("{}", ts["text"].as_str().unwrap_or(""));
    Ok(())
}
