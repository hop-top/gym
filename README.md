# gym vanity module site (Cloudflare Pages)

This directory is published to the `vanity` branch by GitHub Actions and can be deployed with Cloudflare Pages.

Behavior:
- `?go-get=1` => returns Go vanity `go-import` metadata (and optional `go-source`)
- otherwise => HTTP 302 to `https://github.com/hop-top/gym`

## Cloudflare Pages setup

1. Create a Pages project from the `hop-top/gym` repo.
2. Set production branch to `vanity`.
3. Build command: none
4. Output directory: `.`
5. Attach custom domain(s):
   - `gym.hop.top` for vanity import `gym.hop.top`
   - `hop.top` for vanity import `hop.top/gym` (requires the Pages project to own the whole hostname)

## Environment variables (optional)

- `VANITY_REPO`: canonical VCS repo root (default: `https://github.com/hop-top/gym`)
- `VANITY_PREFIX`: comma-separated vanity prefixes (overrides auto-detection)

