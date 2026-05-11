// A backend client that opens a TCP connection, sends an HTTP
// request, and reads the response. The bug: no retry on transient
// errors. An ECONNRESET on the recv path propagates as a hard
// failure.
import net from "node:net";

export function fetchStatusLine(host, port, path = "/") {
  return new Promise((resolve, reject) => {
    const sock = net.createConnection({ host, port }, () => {
      sock.write(
        `GET ${path} HTTP/1.1\r\nHost: ${host}\r\nConnection: close\r\n\r\n`,
      );
    });
    const chunks = [];
    sock.on("data", (c) => chunks.push(c));
    sock.on("end", () => {
      const body = Buffer.concat(chunks).toString();
      resolve(body.split("\r\n", 1)[0]);
    });
    sock.on("error", reject);
    sock.setTimeout(5000, () => sock.destroy(new Error("timeout")));
  });
}
