# Contributing

Thanks for taking the time to contribute.

## Development

Prereqs:
- Go 1.26.1+
- `git`

Build:

```bash
go build -o gym ./cmd/gym
```

Run tests:

```bash
go test ./...
```

Formatting:

```bash
gofmt -w .
```

## Pull Requests

- Target `next` (the default branch). Fixes for the current stable release go to `main`;
  see [RELEASING.md](RELEASING.md).
- Keep changes focused; avoid drive-by refactors.
- Add/adjust tests when changing behavior.
- If you add a new command/flag, update `README.md`.

## Reporting Issues

Bug reports are most useful with:
- command + exact args
- expected vs actual behavior
- `gym version`
- OS + Go version
