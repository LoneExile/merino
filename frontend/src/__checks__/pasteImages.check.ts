import assert from "node:assert/strict";
import { isLineLeading, pasteImageKey, splitPasteImages } from "../pasteImages.ts";

const abs =
  "/Users/lex/Library/Caches/merino/paste/paste-1785215834288577000.jpg";
const home = "~/Library/Caches/merino/paste/paste-1785215834288577000.jpg";

function imgCount(text: string): number {
  return splitPasteImages(text).filter((p) => p.kind === "img").length;
}

assert.equal(imgCount(`${abs}\nHow many donuts?\n`), 1);

{
  const text = `${abs}\nHow many donuts?\n\nRead ${home}\n`;
  assert.equal(imgCount(text), 1, JSON.stringify(splitPasteImages(text)));
}

assert.equal(imgCount(`Read ${abs}\n`), 0);

{
  const p2 = "/Users/lex/Library/Caches/merino/paste/paste-999.jpg";
  assert.equal(imgCount(`${abs}\n${p2}\n`), 2);
}

assert.equal(imgCount(`${abs}\n${home}\n`), 1);
assert.equal(isLineLeading("x\n  /a", 4), true);
assert.equal(isLineLeading("Read /a", 5), false);
assert.equal(pasteImageKey(abs), pasteImageKey(home));

console.log("pasteImages.check.ts: ok");
