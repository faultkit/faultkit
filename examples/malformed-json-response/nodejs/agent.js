// A tool-calling agent. Asks the model which tool to invoke and
// parses the JSON result. The bug: the agent assumes the model
// always returns valid JSON with the expected shape; there is no
// validation of the parsed result.
export async function decideTool(client, query) {
  const resp = await client.chat.completions.create({
    model: "gpt-4o-mini",
    messages: [
      {
        role: "system",
        content:
          'Reply with JSON only: {"tool": "<name>", "args": {"<key>": "<value>"}}',
      },
      { role: "user", content: query },
    ],
  });
  return JSON.parse(resp.choices[0].message.content);
}
