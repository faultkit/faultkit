import { test } from "node:test";
import assert from "node:assert/strict";
import OpenAI from "openai";
import { setGlobalDispatcher, ProxyAgent } from "undici";

import { ask } from "./agent.js";
import { serveInBackground } from "./mock-server.js";

// Node's globalThis.fetch (undici) skips proxy for localhost by
// default, so under faultkit run the local mock would be reached
// directly without going through HTTPS_PROXY. Setting a global
// ProxyAgent forces every request through it. In production
// (api.openai.com over real network) this isn't needed.
const proxy = process.env.HTTPS_PROXY ?? process.env.HTTP_PROXY;
if (proxy) {
  setGlobalDispatcher(new ProxyAgent(proxy));
}

test("ask returns answer", async () => {
  const { port, shutdown } = await serveInBackground();
  try {
    const client = new OpenAI({
      apiKey: "test-key",
      baseURL: `http://127.0.0.1:${port}/v1`,
      maxRetries: 0,
    });
    const answer = await ask(client, "what is the answer?");
    assert.match(answer, /42/);
  } finally {
    await shutdown();
  }
});
