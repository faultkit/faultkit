"""
Streaming agent. Concatenates chunks into a final answer. The bug: the
agent stops when the stream ends, regardless of whether the stream
terminated cleanly. A connection that drops mid-stream produces a
truncated answer that the agent treats as final.
"""

from openai import OpenAI


def stream_answer(client: OpenAI, question: str) -> str:
    stream = client.chat.completions.create(
        model="gpt-4o-mini",
        messages=[{"role": "user", "content": question}],
        stream=True,
    )
    parts = []
    for chunk in stream:
        if chunk.choices and chunk.choices[0].delta.content:
            parts.append(chunk.choices[0].delta.content)
    return "".join(parts)
