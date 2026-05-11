// A file-reading tool an agent might invoke. The bug: the broad
// catch swallows EACCES along with everything else and returns an
// empty string. The agent's loop can't distinguish "file empty"
// from "permission denied".
import fs from "node:fs";

export function readFileTool(path) {
  try {
    return fs.readFileSync(path, "utf8");
  } catch {
    return "";
  }
}
