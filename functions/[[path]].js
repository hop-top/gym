// Cloudflare Pages Function: serve Go vanity import metadata when requested with ?go-get=1.
// Otherwise redirect to the canonical GitHub repo until a landing page exists.

const DEFAULT_REPO = "https://github.com/hop-top/gym";

function htmlEscape(s) {
  return String(s)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;");
}

function vanityPrefixesFor(url, env) {
  if (env && env.VANITY_PREFIX) {
    return String(env.VANITY_PREFIX)
      .split(",")
      .map((p) => p.trim())
      .filter(Boolean);
  }

  // Supports either vanity style:
  // - gym.hop.top (module root at host)
  // - hop.top/gym (module root at /gym)
  const host = url.hostname;
  const path = url.pathname || "/";
  const prefixes = new Set();

  prefixes.add(host);
  if (path === "/gym" || path.startsWith("/gym/")) {
    prefixes.add(host + "/gym");
  }

  // If you attach both domains to the same Pages project, these help too.
  // They are harmless if the host doesn't match.
  prefixes.add("gym.hop.top");
  prefixes.add("hop.top/gym");

  return Array.from(prefixes);
}

function vanityHTML(prefixes, repoRoot) {
  const tags = prefixes
    .map((p) => p.trim())
    .filter(Boolean)
    .map((p) => `<meta name="go-import" content="${htmlEscape(p)} git ${htmlEscape(repoRoot)}">`)
    .join("\n");

  // go-source is optional; keep it simple (GitHub default tree/blob templates).
  const src = prefixes
    .map((p) => p.trim())
    .filter(Boolean)
    .map(
      (p) =>
        `<meta name="go-source" content="${htmlEscape(p)} ${htmlEscape(repoRoot)} ${htmlEscape(
          repoRoot
        )}/tree/main{/dir} ${htmlEscape(repoRoot)}/blob/main{/dir}/{file}#L{line}">`
    )
    .join("\n");

  return `<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
${tags}
${src}
    <title>gym (Go module)</title>
  </head>
  <body>
    <p>Go vanity import metadata for gym. Repo: <a href="${htmlEscape(repoRoot)}">${htmlEscape(repoRoot)}</a></p>
  </body>
</html>
`;
}

export async function onRequest({ request, env }) {
  const url = new URL(request.url);
  const goGet = url.searchParams.get("go-get");
  const isGoGet = goGet === "1" || goGet === "true";

  const repoRoot = (env && env.VANITY_REPO) || DEFAULT_REPO;
  const prefixes = vanityPrefixesFor(url, env);

  if (isGoGet) {
    return new Response(vanityHTML(prefixes, repoRoot), {
      status: 200,
      headers: {
        "content-type": "text/html; charset=utf-8",
        "cache-control": "no-cache",
      },
    });
  }

  // Until a landing page exists, send humans to GitHub.
  return Response.redirect(repoRoot, 302);
}

