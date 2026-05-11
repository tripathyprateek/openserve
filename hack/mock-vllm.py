#!/usr/bin/env python3
"""
mock-vllm.py — Minimal vLLM-compatible server for local openserve development.

Implements the bare minimum to exercise the full gateway → backend flow:
  GET  /health                      → {"status": "ok"}
  GET  /v1/models                   → {"object": "list", "data": [...]}
  POST /v1/chat/completions         → SSE stream (data: chunks + data: [DONE])
  POST /v1/load_lora_adapter        → {"status": "ok"}  (operator reconciler call)
  GET  /metrics                     → Prometheus text with vllm_num_requests_running
"""

import json
import time
import threading
from flask import Flask, request, Response, jsonify

app = Flask(__name__)

# Track in-flight requests for the Prometheus metric so KEDA can observe activity.
_inflight_lock = threading.Lock()
_inflight = 0

MODELS = [
    {
        "id": "llama-3-8b-instruct",
        "object": "model",
        "created": 1715000000,
        "owned_by": "openserve-mock",
    }
]

FAKE_WORDS = [
    "The", " quick", " brown", " fox", " jumps", " over", " the", " lazy", " dog", ".",
    " This", " is", " a", " mock", " vLLM", " response", " for", " local", " development", ".",
    " openserve", " is", " running", " end-to-end", " on", " your", " machine", "!",
]


@app.route("/health", methods=["GET"])
def health():
    return jsonify({"status": "ok"})


@app.route("/v1/models", methods=["GET"])
def list_models():
    return jsonify({"object": "list", "data": MODELS})


@app.route("/v1/chat/completions", methods=["POST"])
def chat_completions():
    body = request.get_json(force=True, silent=True) or {}
    model = body.get("model", "llama-3-8b-instruct")
    stream = body.get("stream", False)
    messages = body.get("messages", [])

    # Derive a simple echo from the last user message.
    last_user = next(
        (m["content"] for m in reversed(messages) if m.get("role") == "user"),
        "Hello",
    )
    # Trim and prefix.
    reply_words = (f"Echo: {last_user[:80]} — " + " ".join(FAKE_WORDS)).split()

    created_ts = int(time.time())
    req_id = f"mock-{created_ts}"

    if stream:
        def generate():
            global _inflight
            with _inflight_lock:
                _inflight += 1
            try:
                for i, word in enumerate(reply_words):
                    chunk = {
                        "id": req_id,
                        "object": "chat.completion.chunk",
                        "created": created_ts,
                        "model": model,
                        "choices": [
                            {
                                "index": 0,
                                "delta": {"content": word + (" " if i < len(reply_words) - 1 else "")},
                                "finish_reason": None,
                            }
                        ],
                        # vLLM emits usage in the final chunk (gateway reads this for TPM).
                        "usage": None,
                    }
                    yield f"data: {json.dumps(chunk)}\n\n"
                    time.sleep(0.02)   # 50 tokens/sec cadence

                # Final chunk with usage.
                final = {
                    "id": req_id,
                    "object": "chat.completion.chunk",
                    "created": created_ts,
                    "model": model,
                    "choices": [
                        {
                            "index": 0,
                            "delta": {},
                            "finish_reason": "stop",
                        }
                    ],
                    "usage": {
                        "prompt_tokens": len(last_user.split()),
                        "completion_tokens": len(reply_words),
                        "total_tokens": len(last_user.split()) + len(reply_words),
                    },
                }
                yield f"data: {json.dumps(final)}\n\n"
                yield "data: [DONE]\n\n"
            finally:
                with _inflight_lock:
                    _inflight -= 1

        return Response(
            generate(),
            mimetype="text/event-stream",
            headers={
                "Cache-Control": "no-cache",
                "X-Accel-Buffering": "no",
            },
        )
    else:
        # Non-streaming response.
        full_text = " ".join(reply_words)
        return jsonify(
            {
                "id": req_id,
                "object": "chat.completion",
                "created": created_ts,
                "model": model,
                "choices": [
                    {
                        "index": 0,
                        "message": {"role": "assistant", "content": full_text},
                        "finish_reason": "stop",
                    }
                ],
                "usage": {
                    "prompt_tokens": len(last_user.split()),
                    "completion_tokens": len(reply_words),
                    "total_tokens": len(last_user.split()) + len(reply_words),
                },
            }
        )


@app.route("/v1/load_lora_adapter", methods=["POST"])
def load_lora_adapter():
    body = request.get_json(force=True, silent=True) or {}
    adapter_name = body.get("lora_name", "unknown")
    print(f"[mock-vllm] LoRA adapter loaded: {adapter_name}", flush=True)
    return jsonify({"status": "ok", "lora_name": adapter_name})


@app.route("/metrics", methods=["GET"])
def metrics():
    """Prometheus metrics — KEDA uses vllm_num_requests_running to scale."""
    with _inflight_lock:
        current = _inflight
    body = (
        "# HELP vllm_num_requests_running Number of requests currently being processed.\n"
        "# TYPE vllm_num_requests_running gauge\n"
        f'vllm_num_requests_running{{deployment="llama-3-8b-instruct"}} {current}\n'
    )
    return Response(body, mimetype="text/plain; version=0.0.4")


if __name__ == "__main__":
    import os
    port = int(os.environ.get("PORT", 8000))
    print(f"[mock-vllm] Listening on :{port}", flush=True)
    # threaded=True so SSE streams don't block other requests.
    app.run(host="0.0.0.0", port=port, threaded=True)
