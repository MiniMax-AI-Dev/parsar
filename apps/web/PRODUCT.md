# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Users

Primary users are developers and administrators on engineering teams. They work in a desktop browser during the working day, operating the console: dispatching tasks to AI coding agents, reading run history and audit trails, and managing members, models, credentials, runtimes, and channel connectors. The same people occasionally open the conversation surface to talk directly with an agent. Confirmed by the user on 2026-09-05.

Other audiences (non-technical teammates, external customers) are not confirmed and must not be designed for without asking.

## Product Purpose

Parsar is an open-source, self-hosted control plane for dispatching, managing, and auditing AI coding agents as a team. Tasks arrive from chat (Feishu / Lark today), the web UI, or the API, and results return where they started: a thread, a PR, a webhook. Success is a team that can see every agent run, trust what it did, and steer it without leaving the tools they already use.

The product is in alpha: APIs, schemas, and configs change between commits.

## Positioning

Team-first, not single-player: shared queues, run history, and permissions across a workspace. Pluggable runtimes (Claude Code, Piagent, Codex; more planned) and pluggable surfaces (Feishu / Lark now; Slack, Discord, webhooks on the roadmap). Every run is persisted: prompt, diff, logs, exit code. Fully self-hosted with no telemetry, under an MIT license with no open-core split. Tagline from the README: "Your team's intent, parsed into action."

## Operating Context

- Workspace-scoped admin console. Routes today: agents, runs, conversations, approvals, audit, members, models, credentials (workspace and personal), connections, connectors, capabilities, runtimes and runtime detail, scheduled tasks, usage, settings.
- Account and workspace lifecycle surfaces: setup (first owner account), login, onboarding (create or join the first workspace), invite acceptance, join-workspace landing.
- A conversation surface built on assistant-ui: a chat thread with tool-call cards, working-step display, approval and user-input interaction cards, and a composer.
- Agents run through daemons on developer machines or in sandboxes (Docker on Linux, E2B in the cloud). Pairing a daemon, checking credentials, and testing model connectivity are routine tasks.
- Approvals and user-choice prompts can originate from a runtime and be answered in the web UI or in the chat channel.
- Data the UI carries is operational: run IDs, durations, token counts, exit codes, timestamps, diffs, logs, status pills (queued, running, completed, failed, cancelled, pending, approved, denied).
- A plugin slot system exists; a plugin registered at the `workspace.main` slot takes over the entire page.

## Capabilities and Constraints

- Stack: React 19, Vite, Tailwind CSS v4, Radix UI primitives, assistant-ui for chat, lucide-react icons, react-i18next.
- Every color in TSX must go through a semantic token (`fg-*`, `surface-*`, `line-*`, `danger / warning / success / info / accent`). Raw palette classes are forbidden by ESLint and fail `make check`. A redesign lands by redefining tokens, never by bypassing them. Confirmed by the user.
- Type scale is fixed at eight ticks (`text-2xs` through `text-3xl`); arbitrary sizes are banned by lint.
- Light and dark themes are both required, switchable by the user with a system option. Confirmed by the user.
- Chinese and English are both first-class locales. Any typeface choice must include a CJK-capable stack and be checked in both languages. Confirmed by the user.
- Typefaces are not a commitment: Inter, Space Grotesk, and Geist Mono may be replaced. Confirmed by the user.
- Desktop-first: the shell sets a 960px minimum width; narrower layouts exist only for the conversation surface.
- Reduced-motion preference is honored globally.
- Dev server runs on 127.0.0.1:5173 and proxies `/dev`, `/api`, `/agent-daemon` to the Go server on 127.0.0.1:18080.

## Brand Commitments

- Name: Parsar. Logo assets in `apps/web/public/` (`parsar-logo-light.png`, `parsar-logo-dark.png`, `favicon.png`, `parsar-banner.png`).
- Visual references the user made binding on 2026-09-05: the restrained, premium register of Notion, OpenAI (openai.com / ChatGPT), and Multica (multica.ai). The user's words: "高级的风格" (a refined, premium feel). This pins register and restraint, not a palette or typeface.
- Voice in the README is direct and plain, with short declarative claims.

## Evidence on Hand

- README and CONTRIBUTING at the repo root; OpenAPI-first API.
- Open-source on GitHub under MiniMax-AI-Dev/parsar, MIT license.
- No customer logos, testimonials, benchmarks, pricing, or usage numbers exist. Future work must not fabricate any of these.

## Product Principles

- The task comes first: scanability, state legibility, and familiar affordances outrank expression on every console surface.
- Restraint is the brand: hierarchy through spacing, weight, and a small number of tones, not through color.
- One system, two themes, two languages: every decision must hold in light and dark and in Chinese and English.
- Operational truth stays visible: identifiers, durations, counts, and statuses are typographically distinct and never decorative.
- Consistency is enforced by tokens, so the system must be expressible entirely as token values and shared components.

## Accessibility & Inclusion

Reduced motion is respected. No further product-specific standard has been established.
