# abacus

`abacus` is a simple benchmark for OpenAI-compatible API inference providers.

v1 keeps the core benchmark loop and metrics, drops raw response capture, and uses Charm's terminal UI stack for a warm-up spinner and live benchmark progress bar.

## Features

- Benchmark any OpenAI-compatible `/v1/chat/completions` endpoint
- Configure requests, concurrency, prompt, model, and generation settings
- Send a warm-up request before the run
- Stream responses and measure TTFT, latency, requests/sec, and tokens/sec
- Show an interactive terminal UI with Bubble Tea, Bubbles, and Lip Gloss

## Usage

```bash
go run ./cmd/abacus \
  --base-url http://localhost:8000/v1 \
  --model Qwen/Qwen3-14B \
  --requests 200 \
  --concurrency 12
```

## Structure

- `cmd/abacus` Cobra-based CLI entrypoint
- `internal/app` top-level runtime orchestration
- `internal/benchmark` benchmark engine and metrics aggregation
- `internal/sse` SSE event reader
- `internal/ui` Bubble Tea progress UI
- `internal/report` final summary rendering

### Flags

- `--base-url` Base URL or full chat completions URL
- `--api-key` Bearer token for authenticated endpoints
- `--model` Model name to benchmark
- `--prompt` Prompt text to send
- `--prompt-file` Read prompt text from a file
- `--requests` Total number of requests
- `--concurrency` Number of parallel workers
- `--max-tokens` `max_tokens` per request
- `--temperature` Sampling temperature
- `--stream-include-usage` Ask the backend to include usage in stream events
- `--no-stream-include-usage` Disable usage in streamed responses
- `--quiet` Disable the interactive UI

## Metrics

At the end of a run, `abacus` reports:

- successful requests and success rate
- total wall-clock time
- requests per second
- stream chunks received
- generated tokens and tokens per second when usage is available
- average, p50, and p95 TTFT
- average, p50, and p95 latency

If any streamed response ends with `finish_reason=length`, `abacus` prints a warning after the summary.
