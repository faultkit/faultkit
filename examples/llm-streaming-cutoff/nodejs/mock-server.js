// OpenAI-compatible mock returning an SSE stream that completes
// properly. Under the streaming-cutoff fault, the proxy truncates
// the stream after N data events without sending [DONE].
import http from "node:http";

const TOKENS = ["The", " final", " answer", " is", " 42", "."];

export function serveInBackground() {
  const server = http.createServer((req, res) => {
    if (req.url !== "/v1/chat/completions") {
      res.writeHead(404).end();
      return;
    }
    req.on("data", () => {});
    req.on("end", () => {
      res.writeHead(200, {
        "Content-Type": "text/event-stream",
        "Cache-Control": "no-cache",
        "Connection": "close",
      });
      for (const token of TOKENS) {
        const event = {
          id: "chatcmpl-test",
          object: "chat.completion.chunk",
          model: "gpt-4o-mini",
          choices: [{ index: 0, delta: { content: token }, finish_reason: null }],
        };
        res.write(`data: ${JSON.stringify(event)}\n\n`);
      }
      const final = {
        id: "chatcmpl-test",
        object: "chat.completion.chunk",
        model: "gpt-4o-mini",
        choices: [{ index: 0, delta: {}, finish_reason: "stop" }],
      };
      res.write(`data: ${JSON.stringify(final)}\n\n`);
      res.end("data: [DONE]\n\n");
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
