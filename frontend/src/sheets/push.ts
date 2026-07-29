import type { Client } from "../client";

export type PushStatus = "checking" | "unsupported" | "denied" | "off" | "subscribed";

// Feature-detects rather than trusting client.pushSubscribe alone: a server
// with push enabled says nothing about whether THIS browser can act on it.
export async function checkPushStatus(client: Client | null): Promise<PushStatus> {
  if (!client?.pushSubscribe || !("serviceWorker" in navigator) || !("PushManager" in window)) {
    return "unsupported";
  }
  if (Notification.permission === "denied") return "denied";
  try {
    const registration = await navigator.serviceWorker.ready;
    const subscription = await registration.pushManager.getSubscription();
    return subscription ? "subscribed" : "off";
  } catch {
    return "unsupported";
  }
}

// PushManager.subscribe wants the VAPID public key as a raw byte array, not
// the base64url string the server hands back — the standard conversion
// every Web Push integration needs.
export function urlBase64ToUint8Array(base64Url: string): Uint8Array<ArrayBuffer> {
  const padding = "=".repeat((4 - (base64Url.length % 4)) % 4);
  const base64 = (base64Url + padding).replace(/-/g, "+").replace(/_/g, "/");
  const raw = atob(base64);
  const bytes = new Uint8Array(raw.length);
  for (let i = 0; i < raw.length; i++) bytes[i] = raw.charCodeAt(i);
  return bytes;
}

/** Short label for Settings — never dump a raw User-Agent. */
