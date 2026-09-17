import type { Query } from "@anthropic-ai/claude-agent-sdk";
import { join } from "node:path";

export type WorkspaceReadEvent = { type: "workspace_read"; id: string } & (
  { data_base64: string; truncated: boolean } | { error: "invalid" | "busy" | "unavailable" | "uncertain" }
);

export class WorkspaceReads {
  private query?: Pick<Query, "readFile" | "close">;
  private root = "";
  private stopped = false;
  private pending?: Promise<void>;

  constructor(private readonly emit: (event: WorkspaceReadEvent) => Promise<void>, private readonly abort: AbortController) {}

  bind(query: Pick<Query, "readFile" | "close">, root: string): void { this.query = query; this.root = root; }

  submit(value: Record<string, unknown>): void {
    if (typeof value.id !== "string" || !/^[a-zA-Z0-9-]{1,128}$/.test(value.id)) throw new Error("invalid_request");
    const id = value.id;
    let error: "invalid" | "busy" | "unavailable" | undefined;
    if (Buffer.byteLength(JSON.stringify(value)) > 8192 || Object.keys(value).some(key => !["type", "id", "path", "max_bytes"].includes(key)) ||
        typeof value.path !== "string" || !value.path || /[\x00\\\r\n]/.test(value.path) || value.path.split("/").some(part => !part || part === "." || part === "..") ||
        !Number.isInteger(value.max_bytes) || (value.max_bytes as number) < 1 || (value.max_bytes as number) > 1048576) error = "invalid";
    else if (this.stopped || this.abort.signal.aborted || !this.query) error = "unavailable";
    else if (this.pending) error = "busy";
    if (error) { void this.emit({ type: "workspace_read", id, error }).catch(() => this.abort.abort()); return; }
    const query = this.query!;
    const path = join(this.root, value.path as string);
    const maxBytes = value.max_bytes as number;
    this.pending = this.read(query, id, path, maxBytes).finally(() => { this.pending = undefined; });
  }

  private async read(query: Pick<Query, "readFile" | "close">, id: string, path: string, maxBytes: number): Promise<void> {
    let timer: ReturnType<typeof setTimeout> | undefined;
    const native = query.readFile(path, { maxBytes, encoding: "base64" });
    try {
      const result = await Promise.race([native, new Promise<never>((_, reject) => {
        timer = setTimeout(() => reject(new Error("native read timeout")), 10000);
      })]);
      if (!result || result.encoding !== "base64" || result.absPath !== path || typeof result.contents !== "string" ||
          (result.truncated !== undefined && typeof result.truncated !== "boolean") || result.contents.length > 4 * Math.ceil(maxBytes / 3)) throw new Error("uncertain");
      const bytes = Buffer.from(result.contents, "base64");
      if (bytes.toString("base64") !== result.contents || bytes.length > maxBytes || (result.truncated && bytes.length !== maxBytes)) throw new Error("uncertain");
      await this.emit({ type: "workspace_read", id, data_base64: result.contents, truncated: result.truncated === true });
    } catch {
      this.stopped = true;
      await this.emit({ type: "workspace_read", id, error: "uncertain" }).catch(() => {});
      this.abort.abort();
      // SDK close resolves its retained request waiter; timeout alone is not native settlement.
      query.close();
      await native.catch(() => null);
    } finally { clearTimeout(timer); }
  }

  async close(): Promise<void> { this.stopped = true; await this.pending; }
}
