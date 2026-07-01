import { useTranslation } from "react-i18next"
import { feishuLoginUrl } from "../lib/api-auth"

export function LoginPage() {
  const { t } = useTranslation("common")
  return (
    <main className="flex min-h-screen bg-surface text-fg">
      {/* Left: full-bleed monochrome hero. Hidden on narrow screens so the
          form stays centered there. The faint dot grid sits under the image
          edges to tie it into the geometric language. */}
      <aside className="relative hidden w-[46%] max-w-[620px] shrink-0 overflow-hidden border-r border-line bg-surface-subtle lg:block">
        <div
          aria-hidden
          className="pointer-events-none absolute inset-0 [background-image:radial-gradient(circle,var(--color-line-strong)_1px,transparent_1px)] [background-size:28px_28px] opacity-40"
        />
        <img
          src="/brand/auth-hero.png"
          alt=""
          className="absolute inset-0 h-full w-full object-cover mix-blend-multiply"
        />
        {/* Bottom-left wordmark, Apple-poster style. */}
        <div className="absolute bottom-10 left-10 flex items-center gap-3">
          <img src="/favicon.png" alt="" className="h-8 w-8" />
          <span className="font-display text-lg font-semibold tracking-tight">Parsar</span>
        </div>
      </aside>

      {/* Right: auth form, generously spaced. */}
      <section className="relative flex flex-1 items-center justify-center overflow-hidden px-6 py-16">
        <div
          aria-hidden
          className="pointer-events-none absolute inset-0 [background-image:radial-gradient(circle,var(--color-line-strong)_1px,transparent_1px)] [background-size:28px_28px] [mask-image:radial-gradient(ellipse_70%_60%_at_50%_40%,black,transparent)] opacity-40 lg:hidden"
        />
        <div className="relative w-full max-w-[360px]">
          <div className="mb-10 flex flex-col items-center text-center lg:items-start lg:text-left">
            <div className="mb-6 grid h-14 w-14 place-items-center rounded-2xl border border-line bg-surface shadow-sm lg:hidden">
              <img src="/favicon.png" alt="" className="h-8 w-8" />
            </div>
            <h1 className="font-display text-[34px] font-semibold leading-[1.05] tracking-tight">
              {t("login.title")}
            </h1>
            <p className="mt-3 text-[15px] leading-relaxed text-fg-subtle">{t("login.subtitle")}</p>
          </div>

          <a
            href={feishuLoginUrl()}
            className="flex h-12 w-full items-center justify-center rounded-full bg-surface-emphasis px-5 text-[15px] font-medium text-white shadow-sm transition-all hover:bg-surface-inverse hover:shadow-md focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-fg/20 focus-visible:ring-offset-2 focus-visible:ring-offset-surface active:translate-y-px"
          >
            {t("login.feishuButton")}
          </a>

          <div className="mt-6 space-y-1 text-center text-sm leading-5 text-fg-faint lg:text-left">
            <p>{t("login.firstLoginNote")}</p>
            <p>
              <a href="#" className="underline-offset-2 hover:text-fg-muted hover:underline">
                {t("login.terms")}
              </a>
            </p>
          </div>
        </div>
      </section>
    </main>
  )
}
