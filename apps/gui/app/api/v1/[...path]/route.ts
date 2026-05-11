/**
 * Runtime proxy: forwards all /api/v1/* requests to the control-api.
 *
 * Unlike next.config.js rewrites (build-time), this Route Handler reads
 * CONTROL_API_URL at request time, so Docker runtime env vars work correctly.
 *
 * CONTROL_API_URL defaults to http://localhost:8080 for `pnpm dev` outside Docker.
 */

import { NextRequest, NextResponse } from "next/server";

const UPSTREAM = (process.env.CONTROL_API_URL ?? "http://localhost:8080").replace(/\/$/, "");

async function proxy(request: NextRequest, params: { path: string[] }) {
  const tail = params.path.join("/");
  const target = new URL(`/api/v1/${tail}`, UPSTREAM);
  target.search = request.nextUrl.search;

  // Forward relevant request headers; drop hop-by-hop headers.
  const forwardHeaders = new Headers();
  for (const [key, value] of request.headers.entries()) {
    const lower = key.toLowerCase();
    if (
      lower === "content-type" ||
      lower === "authorization" ||
      lower === "cookie" ||
      lower === "accept"
    ) {
      forwardHeaders.set(key, value);
    }
  }

  let body: BodyInit | undefined;
  if (request.method !== "GET" && request.method !== "HEAD") {
    body = await request.arrayBuffer();
  }

  const upstream = await fetch(target.toString(), {
    method: request.method,
    headers: forwardHeaders,
    body,
  });

  // Relay response headers (skip hop-by-hop).
  const responseHeaders = new Headers();
  for (const [key, value] of upstream.headers.entries()) {
    const lower = key.toLowerCase();
    if (lower !== "transfer-encoding" && lower !== "connection") {
      responseHeaders.set(key, value);
    }
  }

  return new NextResponse(upstream.body, {
    status: upstream.status,
    headers: responseHeaders,
  });
}

export async function GET(request: NextRequest, { params }: { params: { path: string[] } }) {
  return proxy(request, params);
}
export async function POST(request: NextRequest, { params }: { params: { path: string[] } }) {
  return proxy(request, params);
}
export async function PUT(request: NextRequest, { params }: { params: { path: string[] } }) {
  return proxy(request, params);
}
export async function PATCH(request: NextRequest, { params }: { params: { path: string[] } }) {
  return proxy(request, params);
}
export async function DELETE(request: NextRequest, { params }: { params: { path: string[] } }) {
  return proxy(request, params);
}
