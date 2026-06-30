import { useTranslation } from "react-i18next"
import { feishuLoginUrl } from "../lib/api-auth"

export function LoginPage() {
  const { t } = useTranslation("common")
  return (
    <main className="grid min-h-screen place-items-center bg-surface px-6 text-fg">
      <section className="w-full max-w-[380px] rounded-2xl border border-line/80 bg-surface p-8 shadow-[0_1px_2px_rgb(15_23_42/0.04),0_12px_32px_-12px_rgb(15_23_42/0.12)]">
        <div className="mb-8 flex flex-col items-center text-center">
          <div className="mb-4 grid h-12 w-12 place-items-center rounded-2xl border border-line bg-surface shadow-sm">
            <img src="/favicon.png" alt="" className="h-7 w-7" />
          </div>
          <h1 className="font-display text-2xl leading-none">{t("login.title")}</h1>
          <p className="mt-2 text-base leading-relaxed text-fg-subtle">{t("login.subtitle")}</p>
        </div>

        <a
          href={feishuLoginUrl()}
          className="flex h-10 w-full items-center justify-center rounded-lg bg-[#2952F8] px-4 text-base font-medium text-white transition-colors hover:bg-[#1f45db] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[#2952F8]/30"
        >
          {t("login.feishuButton")}
        </a>

        <div className="mt-5 space-y-1 text-center text-sm leading-5 text-fg-faint">
          <p>{t("login.firstLoginNote")}</p>
          <p>
            <a href="#" className="hover:text-fg-muted">
              {t("login.terms")}
            </a>
          </p>
        </div>
      </section>
    </main>
  )
}
