import { describe, it, expect, vi } from "vitest"
import {
  postVerdict,
  readEnv,
  ENV_VARS,
  type Permission,
  type PluginPermissionRequest,
} from "./index.js"
import ParsarPlugin from "./index.js"

// Fake Response just rich enough for what the plugin reads:
// `.status`, `.text()`, `.json()`.
function fakeFetchResponse(opts: {
  status: number
  body: string
  jsonThrows?: boolean
  textThrows?: boolean
}): Response {
  return {
    status: opts.status,
    async text() {
      if (opts.textThrows) {
        throw new Error("text() boom")
      }
      return opts.body
    },
    async json() {
      if (opts.jsonThrows) {
        throw new Error("json() boom")
      }
      return JSON.parse(opts.body)
    },
  } as unknown as Response
}

describe("readEnv", () => {
  it("reads PARSAR_PLUGIN_CALLBACK_URL", () => {
    const env = readEnv({ [ENV_VARS.callbackUrl]: "http://t/dev/_plugin/permission" })
    expect(env.callbackUrl).toBe("http://t/dev/_plugin/permission")
  })

  it("returns undefined when env is unset", () => {
    const env = readEnv({})
    expect(env.callbackUrl).toBeUndefined()
  })
})

describe("postVerdict", () => {
  const samplePayload: PluginPermissionRequest = {
    id: "perm-1",
    type: "bash",
    pattern: "rm -rf /tmp/scratch",
    session_id: "ses_test",
    message_id: "msg_1",
    call_id: "call_1",
    title: "Run cleanup",
    metadata: { cwd: "/tmp/scratch" },
  }

  it("returns deny when callback URL is unset", async () => {
    const fetchSpy = vi.fn()
    const v = await postVerdict(undefined, samplePayload, fetchSpy as unknown as typeof fetch)
    expect(v.status).toBe("deny")
    expect(v.reason).toContain("PARSAR_PLUGIN_CALLBACK_URL")
    expect(fetchSpy).not.toHaveBeenCalled()
  })

  it("returns allow when server replies allow", async () => {
    const fetchImpl = vi.fn(async () =>
      fakeFetchResponse({
        status: 200,
        body: JSON.stringify({ status: "allow", reason: "user approved" }),
      }),
    )
    const v = await postVerdict("http://server/dev/_plugin/permission", samplePayload, fetchImpl as unknown as typeof fetch)
    expect(v.status).toBe("allow")
    expect(v.reason).toBe("user approved")
    expect(fetchImpl).toHaveBeenCalledTimes(1)
    const call = fetchImpl.mock.calls[0]
    expect(call).toBeDefined()
    const [url, init] = call as unknown as [string, RequestInit]
    expect(url).toBe("http://server/dev/_plugin/permission")
    expect(init.method).toBe("POST")
    expect(JSON.parse(init.body as string)).toEqual(samplePayload)
  })

  it("returns deny when server replies deny", async () => {
    const fetchImpl = vi.fn(async () =>
      fakeFetchResponse({
        status: 200,
        body: JSON.stringify({ status: "deny", reason: "user rejected" }),
      }),
    )
    const v = await postVerdict("http://server/x", samplePayload, fetchImpl as unknown as typeof fetch)
    expect(v.status).toBe("deny")
    expect(v.reason).toBe("user rejected")
  })

  it("fails closed when fetch rejects", async () => {
    const fetchImpl = vi.fn(async () => {
      throw new Error("ECONNREFUSED")
    })
    const v = await postVerdict("http://server/x", samplePayload, fetchImpl as unknown as typeof fetch)
    expect(v.status).toBe("deny")
    expect(v.reason).toContain("ECONNREFUSED")
  })

  it("fails closed on non-200 status", async () => {
    const fetchImpl = vi.fn(async () =>
      fakeFetchResponse({ status: 503, body: "service unavailable" }),
    )
    const v = await postVerdict("http://server/x", samplePayload, fetchImpl as unknown as typeof fetch)
    expect(v.status).toBe("deny")
    expect(v.reason).toContain("503")
    expect(v.reason).toContain("service unavailable")
  })

  it("fails closed when body is not JSON", async () => {
    const fetchImpl = vi.fn(async () =>
      fakeFetchResponse({
        status: 200,
        body: "not json at all",
        jsonThrows: true,
      }),
    )
    const v = await postVerdict("http://server/x", samplePayload, fetchImpl as unknown as typeof fetch)
    expect(v.status).toBe("deny")
    expect(v.reason).toContain("not JSON")
  })

  it("fails closed when response status field is unknown", async () => {
    const fetchImpl = vi.fn(async () =>
      fakeFetchResponse({
        status: 200,
        body: JSON.stringify({ status: "maybe" }),
      }),
    )
    const v = await postVerdict("http://server/x", samplePayload, fetchImpl as unknown as typeof fetch)
    expect(v.status).toBe("deny")
    expect(v.reason).toContain("unknown status")
  })
})

describe("ParsarPlugin permission.ask hook", () => {
  it("registers a permission.ask hook that mutates output.status", async () => {
    const originalEnv = process.env[ENV_VARS.callbackUrl]
    const originalFetch = globalThis.fetch
    try {
      process.env[ENV_VARS.callbackUrl] = "http://server/dev/_plugin/permission"
      globalThis.fetch = vi.fn(async () =>
        fakeFetchResponse({
          status: 200,
          body: JSON.stringify({ status: "allow", reason: "ok" }),
        }),
      ) as unknown as typeof fetch
      const hooks = await ParsarPlugin.server({} as unknown, {})
      expect(hooks["permission.ask"]).toBeDefined()
      const out = { status: "ask" as const }
      const input: Permission = {
        id: "perm-hook-1",
        type: "bash",
        sessionID: "ses_x",
        title: "demo",
      }
      await hooks["permission.ask"]!(input, out)
      expect(out.status).toBe("allow")
    } finally {
      if (originalEnv === undefined) {
        delete process.env[ENV_VARS.callbackUrl]
      } else {
        process.env[ENV_VARS.callbackUrl] = originalEnv
      }
      globalThis.fetch = originalFetch
    }
  })

  it("denies when env is unset (fail-closed)", async () => {
    const originalEnv = process.env[ENV_VARS.callbackUrl]
    try {
      delete process.env[ENV_VARS.callbackUrl]
      const hooks = await ParsarPlugin.server({} as unknown, {})
      const out = { status: "ask" as const }
      const input: Permission = {
        id: "perm-hook-2",
        type: "bash",
        sessionID: "ses_y",
        title: "demo",
      }
      await hooks["permission.ask"]!(input, out)
      expect(out.status).toBe("deny")
    } finally {
      if (originalEnv === undefined) {
        delete process.env[ENV_VARS.callbackUrl]
      } else {
        process.env[ENV_VARS.callbackUrl] = originalEnv
      }
    }
  })
})

describe("plugin default export shape (opencode v1 compat)", () => {
  it("exports an object with id + server (not a bare function)", () => {
    expect(typeof ParsarPlugin).toBe("object")
    expect(typeof ParsarPlugin.id).toBe("string")
    expect(ParsarPlugin.id.length).toBeGreaterThan(0)
    expect(typeof ParsarPlugin.server).toBe("function")
  })
})
