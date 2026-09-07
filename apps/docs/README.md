# Parsar product documentation

This app contains the public, end-user documentation for Parsar. It is a
standalone Next.js + Fumadocs site so the narrative docs can be deployed
independently from the Go API and the existing Swagger UI at `/docs`.

## Local development

From the repository root:

```bash
pnpm --filter @parsar/docs dev
```

The site is available at `http://localhost:4000`. English is the default
locale (`/`); Chinese pages are under `/zh`.

Useful checks:

```bash
pnpm --filter @parsar/docs typecheck
pnpm --filter @parsar/docs build
```

Content lives in `content/docs`. Add an English page as `name.mdx` and its
Chinese translation as `name.zh.mdx`, then update the corresponding
`meta.json` files when adding or reordering navigation entries.

The app intentionally has no `basePath`: deployment can place it at the root
of a dedicated host, or add a reverse-proxy prefix later without changing the
existing Parsar API/Swagger route.
