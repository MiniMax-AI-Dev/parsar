import { expect, test } from "@playwright/test";
import { json, mockApp, WORKSPACE_ID } from "./helpers/conversation-app";

for (const lang of ["en-US", "zh-CN"]) {
  for (const scenario of ["unknown", "known", "mixed"]) {
    test(`Usage costs: ${scenario}, ${lang}`, async ({ page }) => {
      await mockApp(page, "empty", null);
      await page.addInitScript((language) => localStorage.setItem("parsar.lang", language), lang);
      const unknown = lang === "en-US" ? "Unknown" : "未知";
      const costs = scenario === "unknown" ? [0, null, undefined, -1] : scenario === "known" ? [0.1, 0.2] : [0.1, 0];
      await page.route("**/api/v1/workspaces/*/usage*", (route) => json(route, {
        usage_logs: costs.map((cost, index) => ({
          id: `usage-${index}`, agent_run_id: `run-${index}`, provider: "test", model: "example",
          input_tokens: 10, output_tokens: 2, cost_usd: cost, created_at: "2026-09-10T00:00:00Z",
        })),
      }));
      await page.goto(`/?admin=usage&ws=${WORKSPACE_ID}`);
      const total = page.locator("dl dd").nth(3);
      const model = page.getByRole("list", { name: lang === "en-US" ? "By model" : "按模型" }).getByRole("listitem");
      const recent = page.getByRole("list", { name: lang === "en-US" ? "Recent calls" : "最近调用" });
      const expected = scenario === "unknown" ? unknown : scenario === "known" ? "$0.3000" : "$0.1000*";
      await expect(total).toHaveText(expected);
      await expect(model).toContainText(expected);
      await expect(page.locator("dl dd").nth(1)).toHaveText(String(costs.length * 10));
      await expect(recent.getByRole("listitem")).toHaveCount(costs.length);
      await expect(recent.getByText(unknown, { exact: true })).toHaveCount(scenario === "unknown" ? 4 : scenario === "mixed" ? 1 : 0);
      await expect(page.getByText(lang === "en-US" ? /Zero or missing cost values/ : /零值或缺失的费用/)).toHaveCount(scenario === "known" ? 0 : 1);
      if (scenario === "mixed") {
        await expect(total.locator("span")).toHaveAccessibleName(lang === "en-US" ? /Known costs only/ : /仅汇总已知费用/);
        await expect(recent.getByText("$0.1000", { exact: true })).toBeVisible();
      }
    });
  }
}
