import type { Session } from "../client";

export function displaySessionName(session: Session | null | undefined): string {
  if (!session) return "—";
  const u = session.user?.trim() || "";
  if (session.provider === "pairing" || /mozilla|applewebkit|android|iphone/i.test(u)) {
    return friendlyUA(u) || "Phone";
  }
  return u || "—";
}

function friendlyUA(ua: string): string {
  const l = ua.toLowerCase();
  if (l.includes("iphone") || l.includes("crios") || l.includes("fxios")) return "iPhone";
  if (l.includes("ipad")) return "iPad";
  if (l.includes("android")) return l.includes("tablet") ? "Android tablet" : "Android phone";
  if (l.includes("mac")) return "Mac browser";
  if (l.includes("windows")) return "Windows browser";
  if (!ua || /mozilla|webkit/i.test(ua)) return "Phone";
  return ua.length > 28 ? `${ua.slice(0, 28)}…` : ua;
}

export function displayDeviceName(name: string): string {
  if (/mozilla|applewebkit|android|iphone/i.test(name)) return friendlyUA(name);
  return name || "Phone";
}
