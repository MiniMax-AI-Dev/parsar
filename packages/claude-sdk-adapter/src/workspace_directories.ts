import { constants } from "node:fs";
import { lstat, open, opendir, type FileHandle } from "node:fs/promises";

export type WorkspaceDirectoryEntry = { name: string; kind: "file" | "directory" | "symlink" | "other"; size_bytes?: number };
export type WorkspaceDirectoryEvent = { type: "workspace_directory"; id: string } & (
  { entries: WorkspaceDirectoryEntry[]; truncated: boolean } |
  { error: "invalid" | "busy" | "unavailable" | "uncertain" | "not_found" | "permission" }
);
const flags = constants.O_RDONLY | constants.O_DIRECTORY | constants.O_NOFOLLOW;
const anchored = (handle: FileHandle) => `/proc/self/fd/${handle.fd}`;

export class WorkspaceDirectories {
  private root?: FileHandle;
  private stopped = false;
  private pending?: Promise<void>;

  constructor(private readonly emit: (event: WorkspaceDirectoryEvent) => Promise<void>, private readonly abort: AbortController) {}

  async bind(root: string): Promise<void> {
    if (process.platform !== "linux" || this.root || this.stopped) throw new Error("directory listing unavailable");
    this.root = await open(root, flags);
  }

  submit(value: Record<string, unknown>): void {
    if (typeof value.id !== "string" || !/^[a-zA-Z0-9-]{1,128}$/.test(value.id)) throw new Error("invalid_request");
    const id = value.id;
    let error: "invalid" | "busy" | "unavailable" | undefined;
    if (Buffer.byteLength(JSON.stringify(value)) > 8192 || Object.keys(value).some(key => !["type", "id", "directory", "max_entries"].includes(key)) ||
        typeof value.directory !== "string" || /[\x00\\\r\n]/.test(value.directory) ||
        (value.directory !== "" && value.directory.split("/").some(part => !part || part === "." || part === "..")) ||
        !Number.isInteger(value.max_entries) || (value.max_entries as number) < 1 || (value.max_entries as number) > 1000) error = "invalid";
    else if (this.stopped || this.abort.signal.aborted || !this.root) error = "unavailable";
    else if (this.pending) error = "busy";
    if (error) { void this.emit({ type: "workspace_directory", id, error }).catch(() => this.abort.abort()); return; }
    this.pending = this.list(id, value.directory as string, value.max_entries as number).finally(() => { this.pending = undefined; });
  }

  private async list(id: string, directory: string, maxEntries: number): Promise<void> {
    try {
      const result = await this.enumerate(directory, maxEntries);
      await this.emit({ type: "workspace_directory", id, ...result });
    } catch (error) {
      const code = (error as NodeJS.ErrnoException).code;
      const classified = code === "ENOENT" ? "not_found" : code === "ENOTDIR" ? "invalid" :
        ["EACCES", "EPERM", "ELOOP"].includes(code ?? "") ? "permission" : "uncertain";
      await this.emit({ type: "workspace_directory", id, error: classified }).catch(() => this.abort.abort());
      if (classified === "uncertain") { this.stopped = true; this.abort.abort(); }
    }
  }

  private async enumerate(directory: string, maxEntries: number): Promise<{ entries: WorkspaceDirectoryEntry[]; truncated: boolean }> {
    const handles: FileHandle[] = [];
    try {
      let parent = this.root!;
      for (const component of directory === "" ? [] : directory.split("/")) {
        parent = await open(`${anchored(parent)}/${component}`, flags);
        handles.push(parent);
      }
      // Node supports byte names, but its opendir typings omit the buffer encoding.
      const dir = await opendir(anchored(parent), { encoding: "buffer" as BufferEncoding });
      try {
        const entries: WorkspaceDirectoryEntry[] = [];
        while (true) {
          if (this.abort.signal.aborted) throw new Error("owner closed");
          const entry = await dir.read();
          if (!entry) return { entries, truncated: false };
          if (entries.length === maxEntries) return { entries, truncated: true };
          const rawName: unknown = entry.name;
          if (!Buffer.isBuffer(rawName)) throw new Error("invalid entry name");
          const name = rawName.toString("utf8");
          if (!Buffer.from(name).equals(rawName) || !name || /[\x00/]/.test(name) || name === "." || name === "..") throw new Error("unsupported entry name");
          const stat = await lstat(`${anchored(parent)}/${name}`);
          const kind = stat.isFile() ? "file" : stat.isDirectory() ? "directory" : stat.isSymbolicLink() ? "symlink" : "other";
          if (kind === "file" && (!Number.isSafeInteger(stat.size) || stat.size < 0)) throw new Error("invalid file size");
          entries.push({ name, kind, ...(kind === "file" ? { size_bytes: stat.size } : {}) });
        }
      } finally { await dir.close(); }
    } finally {
      const closed = await Promise.allSettled(handles.reverse().map(handle => handle.close()));
      if (closed.some(result => result.status === "rejected")) throw new Error("directory close failed");
    }
  }

  async close(): Promise<void> {
    this.stopped = true;
    await this.pending;
    await this.root?.close();
    this.root = undefined;
  }
}
