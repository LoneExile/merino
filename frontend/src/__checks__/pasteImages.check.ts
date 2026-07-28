import assert from "node:assert/strict";
import { clearImageCache, rememberImage } from "../imageCache.ts";
import {
  collapseKittyBlankLines,
  imageSrcForPath,
  isReadImageLine,
  pasteImageKey,
  splitPasteImages,
} from "../pasteImages.ts";
import { stripAnsi } from "../termSearch.ts";

const abs =
  "/Users/lex/Library/Caches/merino/paste/paste-1785218725441449000.jpg";
const home = "~/Library/Caches/merino/paste/paste-1785218725441449000.jpg";
const gen =
  "~/generated-images/20260728_131646_A_delicious_glazed_donut_with_colorful_s.jpg";

function pieces(t: string) {
  return splitPasteImages(t);
}
function imgs(t: string) {
  return pieces(t).filter((p) => p.kind === "img");
}
function plain(t: string) {
  return stripAnsi(
    pieces(t)
      .filter((p): p is { kind: "text"; text: string } => p.kind === "text")
      .map((p) => p.text)
      .join(""),
  );
}

// blanks
assert.ok(
  collapseKittyBlankLines("a\n" + (" ".repeat(80) + "\n").repeat(40) + "b\n").split("\n")
    .length < 8,
);

// Bare user path stays text — image only after Read (like terminal)
{
  const t = `${abs}\nWhat is this?\n`;
  assert.equal(imgs(t).length, 0);
  assert.ok(plain(t).includes("paste-1785218725441449000"));
  assert.ok(plain(t).includes("What is this?"));
}

// Shell keeps path, no image
{
  const t = `$ file "${abs}" && md5 "${abs}"\n`;
  assert.equal(imgs(t).length, 0);
  assert.ok(plain(t).includes("paste-1785218725441449000"));
}

// Read → image ABOVE Read line; Read text kept
{
  const t = `Looking now.\n \uf111 Read ${home}\nDonuts.\n`;
  assert.equal(imgs(t).length, 1);
  const order = pieces(t).map((p) => p.kind);
  // text … img … text(with Read)
  assert.ok(order.includes("img"));
  assert.ok(/\bRead\b/.test(plain(t)), "Read line stays like terminal");
  assert.ok(plain(t).includes("paste-1785218725441449000") || plain(t).includes("~/Library"));
  assert.ok(plain(t).includes("Donuts."));
}

// ANSI + icon Read
{
  const t = ` \uf111 \x1b[35mRead\x1b[0m ${home}\n`;
  assert.equal(imgs(t).length, 1);
  assert.ok(/\bRead\b/.test(plain(t)));
}

// Saved-to alone does NOT inject (wait for Read, like Kitty)
{
  const t = `Image saved to: ${gen}\nHere's your donut:\n`;
  assert.equal(imgs(t).length, 0);
}

// Full flow: bare path + blanks + Read x2 → 1 image, path/Read kept
{
  const blanks = (" ".repeat(200) + "\n").repeat(44);
  const t =
    `${abs}\nWhat is this?\nLooking now.\n` +
    blanks +
    ` \uf111 Read ${home}\nAnswer\n` +
    blanks +
    ` \uf111 Read ${home}\nMore\n`;
  assert.equal(imgs(t).length, 1);
  const v = plain(t);
  assert.ok(v.includes("What is this?"));
  assert.ok(/\bRead\b/.test(v));
  assert.ok(v.includes("Answer"));
  assert.ok(!/\n{10,}/.test(v));
}

// isReadImageLine
assert.equal(isReadImageLine(` \uf111 Read ${home}`, 8, 8 + home.length), true);
assert.equal(isReadImageLine(abs, 0, abs.length), false);

// cache
clearImageCache();
rememberImage("paste-1.jpg", "data:image/jpeg;base64,xx");
assert.equal(
  imageSrcForPath("/x/merino/paste/paste-1.jpg", "paste-1.jpg"),
  "data:image/jpeg;base64,xx",
);
clearImageCache();

assert.equal(pasteImageKey(abs), pasteImageKey(home));

console.log("pasteImages.check.ts: ok");
