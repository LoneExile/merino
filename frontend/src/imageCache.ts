/**
 * In-tab cache of paste basenames → object URLs (or data URLs).
 *
 * Phone attach already has a blob URL for the chip preview. The pane stream
 * only has the host path string; without this cache, the <img> must hit
 * /api/paste/… which can 404 (gate, race, stale name) and still reserve a
 * tall empty box via max-height CSS.
 *
 * Keyed by basename lowercased so ~/… and /Users/…/paste-N.jpg collapse.
 */

const cache = new Map<string, string>();

export function imageCacheKey(pathOrName: string): string {
  const base = pathOrName.split(/[/\\]/).pop() || pathOrName;
  return base.toLowerCase();
}

export function rememberImage(pathOrName: string, src: string): void {
  if (!src) return;
  cache.set(imageCacheKey(pathOrName), src);
}

export function lookupImage(pathOrName: string): string | undefined {
  return cache.get(imageCacheKey(pathOrName));
}

export function clearImageCache(): void {
  for (const src of cache.values()) {
    if (src.startsWith("blob:")) {
      try {
        URL.revokeObjectURL(src);
      } catch {
        /* ignore */
      }
    }
  }
  cache.clear();
}
