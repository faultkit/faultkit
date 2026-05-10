import { test } from "node:test";
import assert from "node:assert/strict";
import OpenAI from "openai";
import { setGlobalDispatcher, ProxyAgent } from "undici";

import { streamAnswer } from "./agent.js";
import { serveInBackground } from "./mock-server.js";

const proxy = process.env.HTTPS_PROXY ?? process.env.HTTP_PROXY;
if (proxy) {
  setGlobalDispatcher(new ProxyAgent(proxy));
}

test("streamAnswer completes with full sentence", async () => {
  const { port, shutdown } = await serveInBackground();
  try {
    const client = new OpenAI({
      apiKey: "test-key",
      baseURL: `http://127.0.0.1:${port}/v1`,
      maxRetries: 0,
    });
    const answer = await streamAnswer(client, "what is the answer?");
    assert.ok(answer.endsWith("."), `expected sentence end, got ${JSON.stringify(answer)}`);
    assert.match(answer, /42/);
  } finally {
    await shutdown();
  }
});
