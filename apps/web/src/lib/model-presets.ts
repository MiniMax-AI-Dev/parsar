/**
 * Hardcoded provider + model presets for the Add-Model dialog.
 *
 * Hand-maintained. Base URLs / model ids were seeded from models.dev
 * (https://models.dev) but this file is the source of truth now — edit it
 * directly to add a provider or refresh a model list. No build step.
 *
 * Model ids may go stale as vendors ship; the model-key combobox always
 * lets the user type any id, so a missing preset is never a hard block.
 * Future: a backend GET {base_url}/v1/models proxy could replace these
 * static lists with the key's real, live model list.
 */

export interface ModelPreset {
  id: string
  name: string
  /** Context window in tokens; null when unknown. */
  context: number | null
  /** USD per 1M tokens; null when unpriced. */
  costIn: number | null
  costOut: number | null
  tool: boolean
  reasoning: boolean
  vision: boolean
}

export interface ProviderPreset {
  /** models.dev provider id, used as provider_type. */
  key: string
  name: string
  /** npm adapter package (== Model.adapter). */
  adapter: string
  /** Empty when the first-party SDK already knows the default endpoint. */
  defaultBaseURL: string
  docUrl: string
  /** Gateway-style adapter -> show the custom-headers editor. */
  customHeaders: boolean
  /** anthropic-compatible gateway -> show the auth-scheme selector. */
  authSchemeSelector: boolean
  models: ModelPreset[]
}

export const PROVIDER_CATALOG: ProviderPreset[] = [
  {
    key: "openai",
    name: "OpenAI",
    adapter: "@ai-sdk/openai",
    defaultBaseURL: "https://api.openai.com/v1",
    docUrl: "https://platform.openai.com/docs/models",
    customHeaders: false,
    authSchemeSelector: false,
    models: [
      { id: "gpt-5.5-pro", name: "GPT-5.5 Pro", context: 1050000, costIn: 30, costOut: 180, tool: true, reasoning: true, vision: true },
      { id: "gpt-5.5", name: "GPT-5.5", context: 1050000, costIn: 5, costOut: 30, tool: true, reasoning: true, vision: true },
      { id: "gpt-image-2", name: "gpt-image-2", context: 0, costIn: 5, costOut: 30, tool: false, reasoning: false, vision: true },
      { id: "gpt-5.4-nano", name: "GPT-5.4 nano", context: 400000, costIn: 0.2, costOut: 1.25, tool: true, reasoning: true, vision: true },
      { id: "gpt-5.4-mini", name: "GPT-5.4 mini", context: 400000, costIn: 0.75, costOut: 4.5, tool: true, reasoning: true, vision: true },
      { id: "gpt-5.4", name: "GPT-5.4", context: 1050000, costIn: 2.5, costOut: 15, tool: true, reasoning: true, vision: true },
    ],
  },
  {
    key: "anthropic",
    name: "Anthropic Claude",
    adapter: "@ai-sdk/anthropic",
    defaultBaseURL: "https://api.anthropic.com",
    docUrl: "https://docs.anthropic.com/en/docs/about-claude/models",
    customHeaders: false,
    authSchemeSelector: false,
    models: [
      { id: "claude-fable-5", name: "Claude Fable 5", context: 1000000, costIn: 10, costOut: 50, tool: true, reasoning: true, vision: true },
      { id: "claude-opus-4-8", name: "Claude Opus 4.8", context: 1000000, costIn: 5, costOut: 25, tool: true, reasoning: true, vision: true },
      { id: "claude-opus-4-7", name: "Claude Opus 4.7", context: 1000000, costIn: 5, costOut: 25, tool: true, reasoning: true, vision: true },
      { id: "claude-sonnet-4-6", name: "Claude Sonnet 4.6", context: 1000000, costIn: 3, costOut: 15, tool: true, reasoning: true, vision: true },
      { id: "claude-opus-4-6", name: "Claude Opus 4.6", context: 1000000, costIn: 5, costOut: 25, tool: true, reasoning: true, vision: true },
      { id: "claude-opus-4-5", name: "Claude Opus 4.5 (latest)", context: 200000, costIn: 5, costOut: 25, tool: true, reasoning: true, vision: true },
    ],
  },
  {
    key: "google",
    name: "Google Gemini",
    adapter: "@ai-sdk/google",
    defaultBaseURL: "https://generativelanguage.googleapis.com/v1beta",
    docUrl: "https://ai.google.dev/gemini-api/docs/models",
    customHeaders: false,
    authSchemeSelector: false,
    models: [
      { id: "gemini-3.5-flash", name: "Gemini 3.5 Flash", context: 1048576, costIn: 1.5, costOut: 9, tool: true, reasoning: true, vision: true },
      { id: "gemini-3.1-flash-lite", name: "Gemini 3.1 Flash Lite", context: 1048576, costIn: 0.25, costOut: 1.5, tool: true, reasoning: true, vision: true },
      { id: "gemma-4-31b-it", name: "Gemma 4 31B IT", context: 262144, costIn: null, costOut: null, tool: true, reasoning: true, vision: true },
      { id: "gemma-4-26b-a4b-it", name: "Gemma 4 26B A4B IT", context: 262144, costIn: null, costOut: null, tool: true, reasoning: true, vision: true },
      { id: "gemini-3.1-flash-lite-preview", name: "Gemini 3.1 Flash Lite Preview", context: 1048576, costIn: 0.25, costOut: 1.5, tool: true, reasoning: true, vision: true },
      { id: "gemini-3.1-flash-image-preview", name: "Nano Banana 2", context: 65536, costIn: 0.5, costOut: 60, tool: false, reasoning: true, vision: true },
    ],
  },
  {
    key: "deepseek",
    name: "DeepSeek",
    adapter: "@ai-sdk/openai-compatible",
    defaultBaseURL: "https://api.deepseek.com",
    docUrl: "https://api-docs.deepseek.com/quick_start/pricing",
    customHeaders: true,
    authSchemeSelector: false,
    models: [
      { id: "deepseek-v4-flash", name: "DeepSeek V4 Flash", context: 1000000, costIn: 0.14, costOut: 0.28, tool: true, reasoning: true, vision: false },
      { id: "deepseek-v4-pro", name: "DeepSeek V4 Pro", context: 1000000, costIn: 0.435, costOut: 0.87, tool: true, reasoning: true, vision: false },
      { id: "deepseek-reasoner", name: "DeepSeek Reasoner", context: 1000000, costIn: 0.14, costOut: 0.28, tool: true, reasoning: true, vision: false },
      { id: "deepseek-chat", name: "DeepSeek Chat", context: 1000000, costIn: 0.14, costOut: 0.28, tool: true, reasoning: false, vision: false },
    ],
  },
  {
    key: "moonshotai",
    name: "Moonshot (Kimi)",
    adapter: "@ai-sdk/openai-compatible",
    defaultBaseURL: "https://api.moonshot.ai/v1",
    docUrl: "https://platform.moonshot.ai/docs/api/chat",
    customHeaders: true,
    authSchemeSelector: false,
    models: [
      { id: "kimi-k2.7-code", name: "Kimi K2.7 Code", context: 262144, costIn: 0.95, costOut: 4, tool: true, reasoning: true, vision: true },
      { id: "kimi-k2.7-code-highspeed", name: "Kimi K2.7 Code HighSpeed", context: 262144, costIn: 1.9, costOut: 8, tool: true, reasoning: true, vision: true },
      { id: "kimi-k2.6", name: "Kimi K2.6", context: 262144, costIn: 0.95, costOut: 4, tool: true, reasoning: true, vision: true },
      { id: "kimi-k2.5", name: "Kimi K2.5", context: 262144, costIn: 0.6, costOut: 3, tool: true, reasoning: true, vision: true },
      { id: "kimi-k2-thinking-turbo", name: "Kimi K2 Thinking Turbo", context: 262144, costIn: 1.15, costOut: 8, tool: true, reasoning: true, vision: false },
      { id: "kimi-k2-thinking", name: "Kimi K2 Thinking", context: 262144, costIn: 0.6, costOut: 2.5, tool: true, reasoning: true, vision: false },
    ],
  },
  {
    key: "alibaba",
    name: "Alibaba Qwen",
    adapter: "@ai-sdk/openai-compatible",
    defaultBaseURL: "https://dashscope-intl.aliyuncs.com/compatible-mode/v1",
    docUrl: "https://www.alibabacloud.com/help/en/model-studio/models",
    customHeaders: true,
    authSchemeSelector: false,
    models: [
      { id: "qwen3.7-plus", name: "Qwen3.7 Plus", context: 1000000, costIn: 0.5, costOut: 3, tool: true, reasoning: true, vision: true },
      { id: "qwen3.7-max", name: "Qwen3.7 Max", context: 1000000, costIn: 2.5, costOut: 7.5, tool: true, reasoning: true, vision: false },
      { id: "qwen3.6-flash", name: "Qwen3.6 Flash", context: 1000000, costIn: 0.1875, costOut: 1.125, tool: true, reasoning: true, vision: true },
      { id: "qwen3.6-27b", name: "Qwen3.6 27B", context: 262144, costIn: 0.6, costOut: 3.6, tool: true, reasoning: true, vision: true },
      { id: "qwen3.6-max-preview", name: "Qwen3.6 Max Preview", context: 262144, costIn: 1.3, costOut: 7.8, tool: true, reasoning: true, vision: false },
      { id: "qwen3.6-35b-a3b", name: "Qwen3.6 35B-A3B", context: 262144, costIn: 0.248, costOut: 1.485, tool: true, reasoning: true, vision: true },
    ],
  },
  {
    key: "zhipuai",
    name: "Zhipu GLM",
    adapter: "@ai-sdk/openai-compatible",
    defaultBaseURL: "https://open.bigmodel.cn/api/paas/v4",
    docUrl: "https://docs.z.ai/guides/overview/pricing",
    customHeaders: true,
    authSchemeSelector: false,
    models: [
      { id: "glm-5.2", name: "GLM-5.2", context: 1000000, costIn: 1.4, costOut: 4.4, tool: true, reasoning: true, vision: false },
      { id: "glm-5v-turbo", name: "GLM-5V-Turbo", context: 200000, costIn: 5, costOut: 22, tool: true, reasoning: true, vision: true },
      { id: "glm-5.1", name: "GLM-5.1", context: 200000, costIn: 6, costOut: 24, tool: true, reasoning: true, vision: false },
      { id: "glm-5", name: "GLM-5", context: 204800, costIn: 1, costOut: 3.2, tool: true, reasoning: true, vision: false },
      { id: "glm-4.7-flash", name: "GLM-4.7-Flash", context: 200000, costIn: 0, costOut: 0, tool: true, reasoning: true, vision: false },
      { id: "glm-4.7-flashx", name: "GLM-4.7-FlashX", context: 200000, costIn: 0.07, costOut: 0.4, tool: true, reasoning: true, vision: false },
    ],
  },
  {
    key: "xai",
    name: "xAI Grok",
    adapter: "@ai-sdk/xai",
    defaultBaseURL: "https://api.x.ai/v1",
    docUrl: "https://docs.x.ai/docs/models",
    customHeaders: false,
    authSchemeSelector: false,
    models: [
      { id: "grok-4.3", name: "Grok 4.3", context: 1000000, costIn: 1.25, costOut: 2.5, tool: true, reasoning: true, vision: true },
      { id: "grok-build-0.1", name: "Grok Build 0.1", context: 256000, costIn: 1, costOut: 2, tool: true, reasoning: true, vision: true },
      { id: "grok-imagine-image-quality", name: "Grok Imagine Image Quality", context: 8000, costIn: null, costOut: null, tool: false, reasoning: false, vision: true },
      { id: "grok-4.20-multi-agent-0309", name: "Grok 4.20 Multi-Agent", context: 1000000, costIn: 1.25, costOut: 2.5, tool: false, reasoning: true, vision: true },
      { id: "grok-4.20-0309-non-reasoning", name: "Grok 4.20 (Non-Reasoning)", context: 1000000, costIn: 1.25, costOut: 2.5, tool: true, reasoning: false, vision: true },
      { id: "grok-4.20-0309-reasoning", name: "Grok 4.20 (Reasoning)", context: 1000000, costIn: 1.25, costOut: 2.5, tool: true, reasoning: true, vision: true },
    ],
  },
  {
    key: "mistral",
    name: "Mistral",
    adapter: "@ai-sdk/mistral",
    defaultBaseURL: "https://api.mistral.ai/v1",
    docUrl: "https://docs.mistral.ai/getting-started/models/",
    customHeaders: false,
    authSchemeSelector: false,
    models: [
      { id: "mistral-medium-2604", name: "Mistral Medium 3.5", context: 262144, costIn: 1.5, costOut: 7.5, tool: true, reasoning: true, vision: true },
      { id: "mistral-small-latest", name: "Mistral Small (latest)", context: 256000, costIn: 0.15, costOut: 0.6, tool: true, reasoning: true, vision: true },
      { id: "mistral-small-2603", name: "Mistral Small 4", context: 256000, costIn: 0.15, costOut: 0.6, tool: true, reasoning: true, vision: true },
      { id: "devstral-latest", name: "Devstral 2", context: 262144, costIn: 0.4, costOut: 2, tool: true, reasoning: false, vision: false },
      { id: "devstral-2512", name: "Devstral 2", context: 262144, costIn: 0.4, costOut: 2, tool: true, reasoning: false, vision: false },
      { id: "labs-devstral-small-2512", name: "Devstral Small 2", context: 256000, costIn: 0, costOut: 0, tool: true, reasoning: false, vision: true },
    ],
  },
  {
    key: "groq",
    name: "Groq",
    adapter: "@ai-sdk/groq",
    defaultBaseURL: "https://api.groq.com/openai/v1",
    docUrl: "https://console.groq.com/docs/models",
    customHeaders: false,
    authSchemeSelector: false,
    models: [
      { id: "canopylabs/orpheus-v1-english", name: "Canopy Labs Orpheus V1 English", context: 4000, costIn: null, costOut: null, tool: false, reasoning: false, vision: false },
      { id: "canopylabs/orpheus-arabic-saudi", name: "Canopy Labs Orpheus Arabic Saudi", context: 4000, costIn: null, costOut: null, tool: false, reasoning: false, vision: false },
      { id: "openai/gpt-oss-safeguard-20b", name: "Safety GPT OSS 20B", context: 131072, costIn: 0.075, costOut: 0.3, tool: true, reasoning: true, vision: false },
      { id: "groq/compound", name: "Compound", context: 131072, costIn: null, costOut: null, tool: false, reasoning: false, vision: false },
      { id: "groq/compound-mini", name: "Compound Mini", context: 131072, costIn: null, costOut: null, tool: false, reasoning: false, vision: false },
      { id: "openai/gpt-oss-120b", name: "GPT OSS 120B", context: 131072, costIn: 0.15, costOut: 0.6, tool: true, reasoning: true, vision: false },
    ],
  },
  {
    key: "openrouter",
    name: "OpenRouter",
    adapter: "@openrouter/ai-sdk-provider",
    defaultBaseURL: "https://openrouter.ai/api/v1",
    docUrl: "https://openrouter.ai/models",
    customHeaders: false,
    authSchemeSelector: false,
    models: [
      { id: "sakana/fugu-ultra", name: "Fugu Ultra", context: 1000000, costIn: 5, costOut: 30, tool: true, reasoning: true, vision: true },
      { id: "google/gemini-3.1-flash-image", name: "Nano Banana 2 (Gemini 3.1 Flash Image)", context: 131072, costIn: 0.5, costOut: 3, tool: false, reasoning: true, vision: true },
      { id: "google/gemini-3-pro-image", name: "Nano Banana Pro (Gemini 3 Pro Image)", context: 65536, costIn: 2, costOut: 12, tool: true, reasoning: true, vision: true },
      { id: "cohere/north-mini-code:free", name: "North Mini Code (free)", context: 256000, costIn: 0, costOut: 0, tool: true, reasoning: true, vision: false },
      { id: "z-ai/glm-5.2", name: "GLM-5.2", context: 1048576, costIn: 0.94, costOut: 3, tool: true, reasoning: true, vision: false },
      { id: "openrouter/fusion", name: "Fusion", context: 1000000, costIn: null, costOut: null, tool: false, reasoning: false, vision: false },
    ],
  },
  {
    key: "minimax-coding-plan",
    name: "MiniMax",
    adapter: "@ai-sdk/anthropic",
    defaultBaseURL: "https://api.minimax.io/anthropic/v1",
    docUrl: "https://platform.minimax.io/docs/token-plan/intro",
    customHeaders: false,
    authSchemeSelector: false,
    models: [
      { id: "MiniMax-M3", name: "MiniMax-M3", context: 1000000, costIn: 0, costOut: 0, tool: true, reasoning: true, vision: true },
      { id: "MiniMax-M2.7-highspeed", name: "MiniMax-M2.7-highspeed", context: 204800, costIn: 0, costOut: 0, tool: true, reasoning: true, vision: false },
      { id: "MiniMax-M2.7", name: "MiniMax-M2.7", context: 204800, costIn: 0, costOut: 0, tool: true, reasoning: true, vision: false },
      { id: "MiniMax-M2.5-highspeed", name: "MiniMax-M2.5-highspeed", context: 204800, costIn: 0, costOut: 0, tool: true, reasoning: true, vision: false },
      { id: "MiniMax-M2.5", name: "MiniMax-M2.5", context: 204800, costIn: 0, costOut: 0, tool: true, reasoning: true, vision: false },
      { id: "MiniMax-M2.1", name: "MiniMax-M2.1", context: 204800, costIn: 0, costOut: 0, tool: true, reasoning: true, vision: false },
    ],
  },
]

export function findProvider(key: string): ProviderPreset | undefined {
  return PROVIDER_CATALOG.find((p) => p.key === key)
}

/** Short "200K · $0.14/$0.28" caption for a model, omitting null parts. */
export function modelCaption(m: ModelPreset): string {
  const parts: string[] = []
  if (m.context != null) {
    parts.push(m.context >= 1000 ? `${Math.round(m.context / 1000)}K` : String(m.context))
  }
  if (m.costIn != null && m.costOut != null) {
    parts.push(`$${m.costIn}/$${m.costOut}`)
  }
  return parts.join(" · ")
}
