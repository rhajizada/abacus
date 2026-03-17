# abacus

![Go](https://img.shields.io/badge/Go-1.24+-blue.svg)
![License](https://img.shields.io/badge/License-MIT-green.svg)

**abacus** is a lightweight CLI for benchmarking OpenAI-compatible chat completion APIs. Point it at a provider, choose a model, set request volume and concurrency, and get a live terminal view plus a final latency and throughput summary.

## Why **abacus**?

- Quick benchmark runs against any OpenAI-compatible `/v1/chat/completions` endpoint.
- Streaming-aware metrics including TTFT, latency, requests/sec, and tokens/sec.
- Simple CLI flags for prompts, concurrency, token limits, and auth.

## Quick Start

Build:

```sh
go build -o bin/abacus ./cmd/abacus
```

Run a benchmark:

```sh
./bin/abacus \
  --base-url http://localhost:8000/v1 \
  --model Qwen/Qwen3-14B \
  --requests 100 \
  --concurrency 8
```

Run against an authenticated endpoint:

```sh
./bin/abacus \
  --base-url "$LLAMERO_API_BASE" \
  --api-key "$LLAMERO_API_KEY" \
  --model qwen3.5:2b \
  --requests 50 \
  --concurrency 4 \
  --max-tokens 2048
```

Use a prompt file:

```sh
./bin/abacus \
  --base-url http://localhost:8000/v1 \
  --model Qwen/Qwen3-14B \
  --prompt-file ./prompts/long-context.txt
```

Print the version:

```sh
./bin/abacus --version
```

## Flags

| Flag                        | Description                              |
| --------------------------- | ---------------------------------------- |
| `--base-url`                | Base URL or full chat completions URL    |
| `--api-key`                 | Bearer token for authenticated endpoints |
| `--model`                   | Model name to benchmark                  |
| `--prompt`                  | Prompt text to send                      |
| `--prompt-file`             | Read prompt text from a file             |
| `--requests`                | Total number of requests                 |
| `--concurrency`             | Number of parallel workers               |
| `--max-tokens`              | `max_tokens` per request                 |
| `--temperature`             | Sampling temperature                     |
| `--stream-include-usage`    | Request usage in stream events           |
| `--no-stream-include-usage` | Disable usage requests in stream events  |
| `--quiet`                   | Disable the interactive UI               |
| `--version`, `-V`           | Print version                            |

## Output

During a run, **abacus** shows:

- endpoint and model
- warm-up status
- completed requests
- active workers
- SSE chunk count
- token totals and live token rate when usage is available
- latest observed chunk latency

At the end of a run, **abacus** reports:

- successful requests and success rate
- total wall-clock time
- requests per second
- total stream chunks received
- generated tokens and tokens per second when usage is available
- average, p50, and p95 TTFT
- average, p50, and p95 latency

## Notes

If a streamed response ends with `finish_reason=length`, **abacus** prints a warning suggesting a higher `--max-tokens` value or a shorter prompt.
