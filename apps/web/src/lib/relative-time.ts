import { useTranslation } from "react-i18next"

const MINUTE = 60_000
const HOUR = 60 * MINUTE
const DAY = 24 * HOUR

/**
 * Locale-aware relative-time formatter hook. Returns "—" for null /
 * undefined / invalid input. Locale follows i18next's active language.
 */
export function useRelativeTime() {
  const { t } = useTranslation("common")

  return (iso: string | null | undefined): string => {
    if (!iso) return "—"
    const ms = Date.parse(iso)
    if (isNaN(ms)) return "—"
    const diff = Math.max(0, Date.now() - ms)

    if (diff < MINUTE) {
      return t("relativeTime.justNow")
    }
    if (diff < HOUR) {
      const minutes = Math.round(diff / MINUTE)
      return t("relativeTime.minutesAgo", { count: minutes })
    }
    if (diff < DAY) {
      const hours = Math.round(diff / HOUR)
      return t("relativeTime.hoursAgo", { count: hours })
    }
    const days = Math.round(diff / DAY)
    return t("relativeTime.daysAgo", { count: days })
  }
}
