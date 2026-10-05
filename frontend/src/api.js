// Thin wrapper over the backend JSON API. Everything goes through /api, which
// nginx (prod) or Vite (dev) proxies to the backend.
const BASE = "/api";

async function req(path, opts) {
  const res = await fetch(BASE + path, opts);
  if (!res.ok) {
    let msg = res.statusText;
    try {
      const body = await res.json();
      if (body && body.error) msg = body.error;
    } catch {
      // response had no JSON error body; keep the status text
    }
    throw new Error(msg);
  }
  if (res.status === 204) return null;
  return res.json();
}

export function listRuns(limit = 50) {
  return req(`/test-runs?limit=${limit}`);
}

export function getRun(id) {
  return req(`/test-runs/${id}`);
}

export function createRun(body) {
  return req(`/test-runs`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
}

export function cancelRun(id) {
  return req(`/test-runs/${id}/cancel`, { method: "POST" });
}
