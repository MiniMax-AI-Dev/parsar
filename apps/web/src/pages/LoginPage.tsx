import { useTranslation } from "react-i18next"
import { feishuLoginUrl } from "../lib/api-auth"

export function LoginPage() {
  const { t } = useTranslation("common")
  return (
    <main className="relative grid min-h-screen place-items-center overflow-hidden bg-surface px-6 text-fg">
      {/* Restrained geometric texture: faint CSS dot grid, fading from center. */}
      <div
        aria-hidden
        className="pointer-events-none absolute inset-0 [background-image:radial-gradient(circle,var(--color-line-strong)_1px,transparent_1px)] [background-size:26px_26px] [mask-image:radial-gradient(ellipse_60%_50%_at_50%_42%,black,transparent)] opacity-50"
      />
      <section className="relative w-full max-w-[380px] rounded-2xl border border-line/80 bg-surface p-9 shadow-[0_1px_2px_rgb(0_0_0/0.04),0_20px_48px_-16px_rgb(0_0_0/0.16)]">
        <div className="mb-9 flex flex-col items-center text-center">
          <div className="mb-6 grid h-14 w-14 place-items-center rounded-2xl border border-line bg-surface shadow-sm">
            <img src="/favicon.png" alt="" className="h-8 w-8" />
          </div>
          <h1 className="font-display text-[30px] font-semibold tracking-tight leading-none">{t("login.title")}</h1>
          <p className="mt-3 text-[15px] leading-relaxed text-fg-subtle">{t("login.subtitle")}</p>
        </div>

        <a
          href={feishuLoginUrl()}
          className="flex h-12 w-full items-center justify-center rounded-full bg-surface-emphasis px-5 text-[15px] font-medium text-white shadow-sm transition-all hover:bg-surface-inverse hover:shadow-md focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-fg/20 focus-visible:ring-offset-2 focus-visible:ring-offset-surface active:translate-y-px"
        >
          {t("login.feishuButton")}
        </a>

        <div className="mt-6 space-y-1 text-center text-sm leading-5 text-fg-faint">
          <p>{t("login.firstLoginNote")}</p>
          <p>
            <a href="#" className="underline-offset-2 hover:text-fg-muted hover:underline">
              {t("login.terms")}
            </a>
          </p>
        </div>
      </section>
    </main>
  )
}
