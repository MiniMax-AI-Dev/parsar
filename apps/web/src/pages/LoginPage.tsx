import { useTranslation } from "react-i18next"
import { feishuLoginUrl } from "../lib/api-auth"

export function LoginPage() {
  const { t } = useTranslation("common")
  return (
    <main className="grid min-h-screen place-items-center bg-white px-6 text-slate-900">
      <section className="w-full max-w-[380px] rounded-2xl border border-slate-200 bg-white p-8 shadow-sm">
        <div className="mb-8 flex flex-col items-center text-center">
          <div className="mb-3 grid h-11 w-11 place-items-center rounded-xl bg-slate-900 text-[18px] font-bold text-white">
            T
          </div>
          <h1 className="text-2xl font-semibold tracking-display">{t("login.title")}</h1>
          <p className="mt-2 text-[14px] text-slate-500">{t("login.subtitle")}</p>
        </div>

        <a
          href={feishuLoginUrl()}
          className="flex h-10 w-full items-center justify-center rounded-lg bg-[#2952F8] px-4 text-[14px] font-medium text-white transition-colors hover:bg-[#1f45db] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[#2952F8]/30"
        >
          {t("login.feishuButton")}
        </a>

        <div className="mt-5 space-y-1 text-center text-[12px] leading-5 text-slate-400">
          <p>{t("login.firstLoginNote")}</p>
          <p>
            <a href="#" className="hover:text-slate-600">
              {t("login.terms")}
            </a>
          </p>
        </div>
      </section>
    </main>
  )
}
