# E2B envd process protocol

`process.proto` is copied without changes from E2B runtime commit
`fad70f393e800cee0278669a63976c3aaa00871b`,
`packages/envd/spec/process/process.proto` (Apache-2.0, accompanying LICENSE).
Its SHA-256 is `8edd9358c7dbfcad96796b3f0ed8d14c262b8b14d6bc7d5e84d468941511b8e0`.
The generated file uses protoc 32.1 and protoc-gen-go v1.36.12, matching the
repository's protobuf runtime. Regenerate from this directory:

```sh
protoc --go_out=. --go_opt=paths=source_relative \
  --go_opt=Mprocess.proto=github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox/e2b/envdprocess \
  process.proto
```

Only the maintained Connect client is required at runtime. Do not import the E2B
server monorepo or hand-write Connect framing. The full official message schema is
retained; it does not expose these process operations through public Agents API.
