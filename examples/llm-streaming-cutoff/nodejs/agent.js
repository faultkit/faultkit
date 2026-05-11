// Streaming agent. Concatenates chunks into a final answer. The
// bug: the agent stops when the stream ends, regardless of whether
// the stream terminated cleanly. A connection that drops mid-stream
// produces a truncated answer that the agent treats as final.
export async function streamAnswer(client, question) {
  const stream = await client.chat.completions.create({
    model: "gpt-4o-mini",
    messages: [{ role: "user", content: question }],
    stream: true,
  });
  const parts = [];
  for await (const chunk of stream) {
    const content = chunk.choices?.[0]?.delta?.content;
    if (content) parts.push(content);
  }
  return parts.join("");
}
