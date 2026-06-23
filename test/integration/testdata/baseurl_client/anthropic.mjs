// Real-world base-URL client for faultkit's Anthropic integration tests.
//
// It reads ANTHROPIC_BASE_URL — the env var `faultkit run --base-url` injects —
// and makes a real out-of-process HTTP request to faultkit's origin endpoint,
// exactly as the Anthropic SDK would. No Anthropic SDK and no network to the
// real API are needed: faultkit synthesizes these faults offline, so the
// response the client parses is faultkit's provider-shaped fixture.
//
// Output contract (consumed by proxy_failuremode_test.go):
//   STATUS=<http status>
//   BODY=<raw response body, single line>
const base = process.env.ANTHROPIC_BASE_URL;
if (!base) {
  console.error("NO_BASE_URL");
  process.exit(2);
}

const res = await fetch(`${base}/v1/messages`, {
  method: "POST",
  headers: {
    "content-type": "application/json",
    "x-api-key": "sk-faultkit-test",
    "anthropic-version": "2023-06-01",
  },
  body: JSON.stringify({
    model: "claude-sonnet-4",
    max_tokens: 16,
    messages: [{ role: "user", content: "ping" }],
  }),
});

const text = await res.text();
console.log(`STATUS=${res.status}`);
console.log(`BODY=${text.replace(/\n/g, " ")}`);
process.exit(0);
