import { expect, test, type Page } from "@playwright/test";

async function openToasts(page: Page) {
  await page.addInitScript(() => localStorage.setItem("i18nextLng", "en-US"));
  await page.route("**/toast-regression", (route) => route.fulfill({
    contentType: "text/html",
    body: `<!doctype html><html><body><div id="root"></div>
      <script type="module">
        import RefreshRuntime from '/@react-refresh';
        RefreshRuntime.injectIntoGlobalHook(window);
        window.$RefreshReg$ = () => {};
        window.$RefreshSig$ = () => (type) => type;
        window.__vite_plugin_react_preamble_installed__ = true;
        const { default: React } = await import('/node_modules/.vite/deps/react.js');
        const { default: ReactDOM } = await import('/node_modules/.vite/deps/react-dom_client.js');
        const { ToastProvider, useToast } = await import('/src/components/ui/toast.tsx');
        await import('/src/i18n/index.ts');
        await import('/src/style.css');
        const h = React.createElement;
        function Controls() {
          const { show, dismiss } = useToast();
          return h('div', { style: { paddingTop: '160px' } },
            h('button', { onClick: () => show('Saved') }, 'Show success'),
            h('button', { onClick: () => show('19 deleted, 2 failed', {
              tone: 'error', detail: 'model_in_use\\nmodel_in_use', key: 'result',
            }) }, 'Show error'),
            h('button', { onClick: () => show('Save layout?', {
              key: 'layout', persist: true,
              action: h('button', { onClick: () => dismiss('layout') }, 'Keep layout'),
            }) }, 'Show persistent'),
          );
        }
        ReactDOM.createRoot(document.getElementById('root')).render(h(React.StrictMode, null,
          h(ToastProvider, null, h(Controls))));
      </script></body></html>`,
  }));
  await page.goto("/toast-regression");
  await expect(page.getByRole("button", { name: "Show error", exact: true })).toBeVisible();
}

for (const disabledAnimations of [false, true]) {
  test(`error toast expires while hovered, animations disabled: ${disabledAnimations}`, async ({ page }) => {
    await openToasts(page);
    if (disabledAnimations) await page.addStyleTag({ content: "* { animation: none !important; }" });
    await page.getByRole("button", { name: "Show error", exact: true }).click();
    const message = page.getByText("19 deleted, 2 failed", { exact: true });
    await expect(message).toBeVisible();
    await message.hover();
    await expect(message).toHaveCount(0, { timeout: 10_000 });
    await expect(page.getByText("model_in_use", { exact: false })).toHaveCount(0);
  });
}

test("success toast expires and error toast can be closed immediately", async ({ page }) => {
  await openToasts(page);
  await page.getByRole("button", { name: "Show success", exact: true }).click();
  await expect(page.getByText("Saved", { exact: true })).toBeVisible();
  await expect(page.getByText("Saved", { exact: true })).toHaveCount(0, { timeout: 6000 });
  await page.getByRole("button", { name: "Show error", exact: true }).click();
  await page.getByRole("button", { name: "Close", exact: true }).click();
  await expect(page.getByText("19 deleted, 2 failed", { exact: true })).toHaveCount(0, { timeout: 1000 });
});

test("keyboard focus pauses expiration and blur resumes it", async ({ page }) => {
  await openToasts(page);
  await page.getByRole("button", { name: "Show error", exact: true }).click();
  await page.getByRole("button", { name: "Close", exact: true }).focus();
  await page.waitForTimeout(7500);
  const message = page.getByText("19 deleted, 2 failed", { exact: true });
  await expect(message).toBeVisible();
  await page.getByRole("button", { name: "Show success", exact: true }).focus();
  await expect(message).toHaveCount(0, { timeout: 8000 });
});

test("persistent prompts remain until their action dismisses them", async ({ page }) => {
  await openToasts(page);
  await page.getByRole("button", { name: "Show persistent", exact: true }).click();
  await page.waitForTimeout(7500);
  await expect(page.getByText("Save layout?", { exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Close", exact: true })).toHaveCount(0);
  await page.getByRole("button", { name: "Keep layout", exact: true }).click();
  await expect(page.getByText("Save layout?", { exact: true })).toHaveCount(0);
});
