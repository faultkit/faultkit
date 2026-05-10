import { test } from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";

import { readFileTool } from "./agent.js";

test("reads config", () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "faultkit-"));
  const p = path.join(dir, "config.txt");
  fs.writeFileSync(p, "secret=42\n");
  try {
    assert.equal(readFileTool(p), "secret=42\n");
  } finally {
    fs.rmSync(dir, { recursive: true });
  }
});
