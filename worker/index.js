// agentstats front Worker — thin router (doc 12) + binding proxies for D1 and R2 (doc 13).
//
// Plain JavaScript, no TypeScript, no build step of our own (wrangler bundles the one
// dependency, @cloudflare/containers). The Go container is the whole backend.
//
// Routes (and ONLY these):
//   GET  /badge/* /card/* /og/*   -> stream pre-rendered bytes from R2 (edge-cached; never wakes the container)
//   POST /_internal/d1            -> query D1 through the binding (no REST-API rate limit)
//   *    /_internal/r2            -> PUT/GET/DELETE R2 objects through the binding (no S3 key needed)
//   everything else               -> the Go container
// Both /_internal routes require X-Internal-Secret == INTERNAL_D1_SECRET (a Worker secret).
// No secret VALUES appear in this file.

import { Container } from "@cloudflare/containers";
import { env as workerEnv } from "cloudflare:workers";

// Bump when a deploy must not reuse a still-running old-version instance.
const BACKEND_INSTANCE = "agentstats-2";

export class Analyzer extends Container {
  defaultPort = 8080;
  sleepAfter = "10m"; // warm for the dashboard, short enough that old versions drain after a deploy
  envVars = {
    ADDR: ":8080",
    AGENTSTATS_SITE_URL: workerEnv.SITE_URL,
    INTERNAL_D1_URL: `${workerEnv.SITE_URL}/_internal/d1`,
    INTERNAL_R2_URL: `${workerEnv.SITE_URL}/_internal/r2`,
    INTERNAL_D1_SECRET: workerEnv.INTERNAL_D1_SECRET,
    GITHUB_APP_ID: workerEnv.GITHUB_APP_ID,
    GITHUB_CLIENT_ID: workerEnv.GITHUB_CLIENT_ID,
    GITHUB_CLIENT_SECRET: workerEnv.GITHUB_CLIENT_SECRET,
    GITHUB_APP_PRIVATE_KEY: workerEnv.GITHUB_APP_PRIVATE_KEY,
    SESSION_KEY: workerEnv.SESSION_KEY,
  };
}

export default {
  async fetch(request, env) {
    const path = new URL(request.url).pathname;

    if (path.startsWith("/badge/") || path.startsWith("/card/") || path.startsWith("/og/")) {
      return serveFromR2(env, path);
    }
    if (path === "/_internal/d1") return guard(request, env, () => internalD1(request, env));
    if (path === "/_internal/r2") return guard(request, env, () => internalR2(request, env));

    // Everything else -> the single warm backend instance.
    return env.CONTAINER.getByName(BACKEND_INSTANCE).fetch(request);
  },
};

function guard(request, env, next) {
  const secret = env.INTERNAL_D1_SECRET;
  if (!secret || request.headers.get("X-Internal-Secret") !== secret) {
    return new Response("forbidden", { status: 403 });
  }
  return next();
}

// /badge/<login>.svg | /badge/<login>/<badge_id>.svg | /card/<login>/<file> | /og/<login>.png
async function serveFromR2(env, path) {
  const seg = path.split("/").filter(Boolean);
  let login = "", file = "", versioned = true;
  if (seg[0] === "card") {
    login = seg[1];
    file = seg[2];
  } else if (seg[0] === "badge" && seg.length === 2) {
    login = stripExt(seg[1], ".svg");
    file = "badge.svg";
  } else if (seg[0] === "badge" && seg.length === 3) {
    login = seg[1];
    file = `badges/${seg[2]}`; // custom badges live outside the version dirs
    versioned = false;
  } else if (seg[0] === "og") {
    login = stripExt(seg[1], ".png");
    file = "og.png";
  }
  login = (login || "").toLowerCase();
  if (!login || !file || file.includes("..")) return placeholder(file);

  let key = `u/${login}/${file}`;
  if (versioned) {
    const ptr = await env.CARDS.get(`u/${login}/current.json`);
    if (!ptr) return placeholder(file);
    let v;
    try {
      v = JSON.parse(await ptr.text()).v;
    } catch (_) {
      return placeholder(file);
    }
    key = `u/${login}/v${v}/${file}`;
  }
  const obj = await env.CARDS.get(key);
  if (!obj) return placeholder(file);

  const headers = new Headers({
    "Content-Type": contentType(file),
    "Cache-Control": "public, max-age=3600, s-maxage=21600",
  });
  if (obj.httpEtag) headers.set("ETag", obj.httpEtag);
  return new Response(obj.body, { headers });
}

async function internalD1(request, env) {
  if (request.method !== "POST") return new Response("POST only", { status: 405 });
  let body;
  try {
    body = await request.json();
  } catch (_) {
    return json({ success: false, error: "bad json" }, 400);
  }
  const params = Array.isArray(body.params) ? body.params : [];
  try {
    const res = await env.DB.prepare(body.sql || "").bind(...params).all();
    return json({ success: true, results: res.results || [] });
  } catch (e) {
    return json({ success: false, error: String(e) });
  }
}

async function internalR2(request, env) {
  const q = new URL(request.url).searchParams;
  const key = q.get("key") || "";
  if (request.method === "PUT") {
    if (!key) return json({ ok: false, error: "key required" }, 400);
    await env.CARDS.put(key, request.body, {
      httpMetadata: { contentType: request.headers.get("Content-Type") || "application/octet-stream" },
    });
    return json({ ok: true });
  }
  if (request.method === "GET") {
    const obj = key ? await env.CARDS.get(key) : null;
    if (!obj) return new Response("not found", { status: 404 });
    return new Response(obj.body, { headers: { "Content-Type": obj.httpMetadata?.contentType || "application/octet-stream" } });
  }
  if (request.method === "DELETE") {
    const prefix = q.get("prefix") || "";
    if (!prefix.startsWith("u/")) return json({ ok: false, error: "prefix must start with u/" }, 400);
    let cursor, deleted = 0;
    do {
      const page = await env.CARDS.list({ prefix, cursor });
      const keys = page.objects.map((o) => o.key);
      if (keys.length) await env.CARDS.delete(keys);
      deleted += keys.length;
      cursor = page.truncated ? page.cursor : undefined;
    } while (cursor);
    return json({ ok: true, deleted });
  }
  return new Response("method not allowed", { status: 405 });
}

function stripExt(s, ext) {
  return s && s.endsWith(ext) ? s.slice(0, -ext.length) : s;
}

function contentType(file) {
  if (file.endsWith(".png")) return "image/png";
  if (file.endsWith(".json")) return "application/json";
  return "image/svg+xml";
}

function placeholder(file) {
  const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="480" height="120"><rect width="480" height="120" rx="10" fill="#1a1512"/><text x="24" y="54" fill="#f4f1ea" font-family="system-ui" font-size="18">no stats yet</text><text x="24" y="84" fill="#b9b1a6" font-family="system-ui" font-size="13">agentstats.deemwar.com</text></svg>`;
  return new Response(svg, { headers: { "Content-Type": "image/svg+xml", "Cache-Control": "public, max-age=300" } });
}

function json(obj, status = 200) {
  return new Response(JSON.stringify(obj), { status, headers: { "Content-Type": "application/json" } });
}
