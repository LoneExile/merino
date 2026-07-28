import assert from "node:assert/strict";
import { imageSrcForPath, pasteImageKey, splitPasteImages } from "../pasteImages.ts";

const abs =
  "/Users/lex/Library/Caches/merino/paste/paste-1785215834288577000.jpg";
const home = "~/Library/Caches/merino/paste/paste-1785215834288577000.jpg";
const gen = "~/generated-images/donut.jpg";
const genAbs = "/Users/lex/generated-images/donut.jpg";

function imgs(text: string) {
  return splitPasteImages(text).filter((p) => p.kind === "img");
}
function imgCount(text: string): number {
  return imgs(text).length;
}

assert.equal(imgCount(`${abs}\nHow many donuts?\n`), 1);
assert.equal(imgCount(`${abs}\nHow many?\n\nRead ${home}\n`), 1);
assert.equal(imgCount(`Read ${abs}\n`), 1);

// Agent-generated home image
assert.equal(imgCount(`Read ${gen}\n`), 1);
assert.equal(imgCount(`saved at:\n${genAbs}\n`), 1);
{
  const i = imgs(`Read ${gen}\n`)[0]!;
  assert.equal(i.kind, "img");
  if (i.kind === "img") {
    assert.ok(i.src.includes("/api/local-image?path="), i.src);
  }
}
// merino paste still uses /api/paste/
{
  const i = imgs(`${abs}\n`)[0]!;
  assert.equal(i.kind, "img");
  if (i.kind === "img") {
    assert.ok(i.src.startsWith("/api/paste/"), i.src);
  }
}

// Two different → two images
assert.equal(imgCount(`${abs}\n${gen}\n`), 2);
assert.equal(imgCount(`${abs}\n${home}\n`), 1);
assert.equal(pasteImageKey(abs), pasteImageKey(home));
assert.ok(imageSrcForPath(gen, "donut.jpg").includes("local-image"));

console.log("pasteImages.check.ts: ok");
