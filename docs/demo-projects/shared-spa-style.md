# Shared SPA style for demo products

**Decision (epic 50.02):** all five demo products use **one shared minimal SPA style** —
static HTML + CSS + vanilla JS with `fetch` — not per-product frameworks.

## Why

* Browser automation stays trivial (stable roles/labels, no hydration races).
* Dockerfiles stay small: copy a `public/` tree into nginx (or any static file server).
* No bundler, node_modules, or framework upgrades inside demos.
* Product differences live in markup and API calls, not in build toolchains.

## Conventions

| Concern | Rule |
|---|---|
| Stack | Plain HTML5, one `styles.css`, one `app.js` (or a few ES modules without a bundler) |
| API access | `fetch` against same-origin or `api.<product>.localhost` routes via Gateway |
| Auth | Cookie or bearer token from Identity; store in `sessionStorage` only when needed |
| Markup | Semantic elements + accessible names (`button`, `label`, `table`); Playwright targets roles/text |
| Assets | Everything under `public/`; no CDN frameworks |
| Theming | CSS custom properties on `:root` (background, text, accent, border) |
| State | Small in-memory or `sessionStorage` helpers; no Redux/Vuex/etc. |

## Suggested layout

```text
demos/5X-<name>/
├── public/
│   ├── index.html
│   ├── styles.css
│   └── app.js
├── api/                 # language-specific backend (Go, …)
├── Dockerfile.web       # nginx:alpine COPY public/ → html root
└── demo.json
```

## Explicitly out of style

* React / Vue / Svelte / Angular (or any SPA framework)
* Vite / webpack / esbuild product pipelines
* CSS-in-JS libraries
* Heavy component kits

If a future demo truly needs a richer UI, prefer extending this static pattern first; only
break the rule with an ADR and an epic-level exception.
