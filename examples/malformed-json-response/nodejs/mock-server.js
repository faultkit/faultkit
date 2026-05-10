// OpenAI-compatible mock returning valid inner JSON in the content
// field. Under faultkit's malformed-json-response fault, the
// content gets replaced with intentionally invalid JSON.
import http from "node:http";

const innerPayload = JSON.stringify({ tool: "lookup_user", args: { id: 42 } });

export function serveInBackground() {
  const server = http.createServer((req, res) => {
    if (req.url !== "/v1/chat/completions") {
      res.writeHead(404).end();
      return;
    }
    let body = "";
    req.on("data", (c) => (body += c));
    req.on("end", () => {
      const payload = JSON.stringify({
        id: "chatcmpl-test",
        object: "chat.completion",
        model: "gpt-4o-mini",
        choices: [
          {
            index: 0,
            message: { role: "assistant", content: innerPayload },
            finish_reason: "stop",
          },
        ],
      });
      res.writeHead(200, {
        "Content-Type": "application/json",
        "Content-Length": Buffer.byteLength(payload),
      });
      res.end(payload);
    });
  });
  return new Promise((resolve) => {
    server.listen(0, "127.0.0.1", () => {
      resolve({
        port: server.address().port,
        shutdown: () => new Promise((r) => server.close(r)),
      });
    });
  });
}
