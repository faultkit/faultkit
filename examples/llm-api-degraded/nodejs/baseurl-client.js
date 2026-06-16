// Base-URL mode demo client.
//
// Uses the real `openai` SDK with NO baseURL passed in code: the SDK reads
// OPENAI_BASE_URL from the environment, which `faultkit run --base-url`
// injects. The SDK's HTTP layer (Node fetch / undici) ignores HTTPS_PROXY,
// so faultkit's forward-proxy mode would not intercept it — base-URL mode
// does. Retries are disabled so a single injected 429 surfaces
// deterministically.
//
// Supply chain: the only dependency is `openai`, pinned via package-lock.json
// and installed with `npm ci` (never `npm install`).
import OpenAI from "openai";

const client = new OpenAI({ apiKey: "sk-faultkit-test", maxRetries: 0 });

try {
  await client.chat.completions.create({
    model: "gpt-4o-mini",
    messages: [{ role: "user", content: "ping" }],
  });
  console.log("NO_FAULT");
  process.exit(0);
} catch (err) {
  if (err && err.status === 429) {
    console.log("GOT_429");
    process.exit(7);
  }
  console.error("UNEXPECTED", err && err.status, err && err.message);
  process.exit(2);
}
