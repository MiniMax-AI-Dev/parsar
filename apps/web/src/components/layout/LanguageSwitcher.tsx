import { useTranslation } from "react-i18next"
import { Languages } from "lucide-react"
import { cn } from "../../lib/utils"
import { SUPPORTED_LANGUAGES, type SupportedLanguage } from "../../i18n"

export function LanguageSwitcher() {
  const { i18n, t } = useTranslation("common")
  const current = (i18n.resolvedLanguage ?? "zh-CN") as SupportedLanguage

  return (
    <div className="inline-flex items-center gap-1 rounded-md border border-slate-200 bg-white p-0.5 text-xs">
      <Languages className="ml-1.5 h-3 w-3 text-slate-400" aria-hidden />
      {SUPPORTED_LANGUAGES.map((lang) => {
        const active = current === lang
        return (
          <button
            key={lang}
            type="button"
            onClick={() => void i18n.changeLanguage(lang)}
            className={cn(
              "rounded px-2 py-0.5 transition-colors",
              active
                ? "bg-slate-900 text-white"
                : "text-slate-600 hover:bg-slate-100"
            )}
            title={t(`languageSwitcher.${lang}`)}
          >
            {lang === "zh-CN" ? "中" : "EN"}
          </button>
        )
      })}
    </div>
  )
}
