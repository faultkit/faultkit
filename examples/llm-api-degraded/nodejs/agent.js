// A minimal OpenAI-using agent. Calls chat.completions.create and
// returns the assistant's text. The bug: when the API returns 429,
// the exception propagates — there is no retry, no backoff, no
// fallback.
export async function ask(client, question) {
  const resp = await client.chat.completions.create({
    model: "gpt-4o-mini",
    messages: [{ role: "user", content: question }],
  });
  return resp.choices[0].message.content;
}
