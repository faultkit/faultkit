import { test } from "node:test";
import assert from "node:assert/strict";
import http from "node:http";

import { fetchStatusLine } from "./agent.js";

test("fetch returns 200 status line", async () => {
  const server = http.createServer((_, res) => {
    res.writeHead(200, { "Content-Length": "5" });
    res.end("hello");
  });
  await new Promise((r) => server.listen(0, "127.0.0.1", r));
  const { port } = server.address();
  try {
    const line = await fetchStatusLine("127.0.0.1", port);
    assert.match(line, /200/);
  } finally {
    server.close();
  }
});
