import { expect, test } from "@playwright/test";

import type { ConversationTimelineMessage } from "../../apps/web/src/lib/api-types";
import {
  dedupeCapabilityRuntimeDiagnostics,
  isRuntimeErrorMessage,
} from "../../apps/web/src/pages/admin/conversation-runtime-errors";

function runtimeErrorMessage(
  id: string,
  subKind: string,
  capabilityID: string,
  credentialKind = "",
): ConversationTimelineMessage {
  return {
    id,
    conversation_id: "conversation-1",
    sender_type: "system",
    kind: "error",
    content: subKind,
    metadata: {
      kind: "runtime_error",
      sub_kind: subKind,
      capability_id: capabilityID,
      credential_kind: credentialKind,
    },
    created_at: `2026-07-23T00:00:0${id}.000Z`,
  };
}

test("recognizes persisted runtime errors stored with kind=error", () => {
  expect(isRuntimeErrorMessage("error", { kind: "runtime_error" })).toBe(true);
  expect(isRuntimeErrorMessage("error", { error: { source: "runtime" } })).toBe(
    true,
  );
  expect(isRuntimeErrorMessage("error", { kind: "validation_error" })).toBe(
    false,
  );
});

test("keeps only the newest identical capability diagnostic", () => {
  const first = runtimeErrorMessage(
    "1",
    "capability_credential_missing",
    "notion",
    "notion_mcp_oauth",
  );
  const unrelated = runtimeErrorMessage(
    "2",
    "capability_version_unavailable",
    "pr-review",
  );
  const newest = runtimeErrorMessage(
    "3",
    "capability_credential_missing",
    "notion",
    "notion_mcp_oauth",
  );
  const historicalUnsupported = runtimeErrorMessage(
    "4",
    "capability_credential_missing",
    "diagram-maker",
  );
  const currentUnsupported = runtimeErrorMessage(
    "5",
    "capability_unsupported",
    "diagram-maker",
  );

  expect(
    dedupeCapabilityRuntimeDiagnostics([
      first,
      unrelated,
      newest,
      historicalUnsupported,
      currentUnsupported,
    ]).map((item) => item.id),
  ).toEqual(["2", "3", "5"]);
});
